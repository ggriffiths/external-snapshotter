/*
Copyright 2020 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package metrics

import (
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	crdv1 "github.com/kubernetes-csi/external-snapshotter/client/v3/apis/volumesnapshot/v1beta1"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/apimachinery/pkg/types"
	k8smetrics "k8s.io/component-base/metrics"
	klog "k8s.io/klog/v2"
)

const (
	labelDriverName                    = "driver_name"
	labelOperationName                 = "operation_name"
	labelOperationStatus               = "operation_status"
	labelSnapshotType                  = "snapshot_type"
	subSystem                          = "snapshot_controller"
	latencyReconciliationMetricName    = "reconciliation_total_seconds"
	latencyReconciliationMetricHelpMsg = "Number of seconds spent by the controller on intermediate reconciliations"
	latencyOperationMetricName         = "operation_total_seconds"
	latencyOperationMetricHelpMsg      = "Total number of seconds spent by the controller on an operation"

	// CreateSnapshotOperationName is the operation that tracks how long the controller takes to create a snapshot.
	// Specifically, the operation metric is emitted based on the following timestamps:
	// - Start_time: controller notices the first time that there is a new VolumeSnapshot CR to dynamically provision a snapshot
	// - End_time:   controller notices that the CR has a status with CreationTime field set to be non-nil OR an error occurs first
	CreateSnapshotOperationName = "CreateSnapshot"

	// CreateSnapshotAndReadyOperationName is the operation that tracks how long the controller takes to create a snapshot and for it to be ready.
	// Specifically, the operation metric is emitted based on the following timestamps:
	// - Start_time: controller notices the first time that there is a new VolumeSnapshot CR(both dynamic and pre-provisioned cases)
	// - End_time:   controller notices that the CR has a status with Ready field set to be true OR an error occurs first
	CreateSnapshotAndReadyOperationName = "CreateSnapshotAndReady"

	// DeleteSnapshotOperationName is the operation that tracks how long a snapshot deletion takes.
	// Specifically, the operation metric is emitted based on the following timestamps:
	// - Start_time: controller notices the first time that there is a deletion timestamp placed on the VolumeSnapshot CR and the CR is ready to be deleted. Note that if the CR is being used by a PVC for rehydration, the controller should *NOT* set the start_time.
	// - End_time: controller removed all finalizers on the VolumeSnapshot CR such that the CR is ready to be removed in the API server.
	DeleteSnapshotOperationName = "DeleteSnapshot"

	// DynamicSnapshotType represents a snapshot that is being dynamically provisioned
	DynamicSnapshotType = snapshotProvisionType("dynamic")
	// PreProvisionedSnapshotType represents a snapshot that is pre-provisioned
	PreProvisionedSnapshotType = snapshotProvisionType("pre-provisioned")

	SnapshotStatusTypeUnknown            = snapshotStatusType("unknown")
	SnapshotStatusTypeSuccess            = snapshotStatusType("success")
	SnapshotStatusTypeInvalidRequest     = snapshotStatusType("invalid-request")
	SnapshotStatusTypeControllerError    = snapshotStatusType("controller-error")
	SnapshotStatusTypeStorageSystemError = snapshotStatusType("storage-system-error")
	SnapshotStatusTypeCancel             = snapshotStatusType("cancel")
)

// OperationStatus is the interface type for representing an operation's execution
// status, with the nil value representing an "Unknown" status of the operation.
type OperationStatus interface {
	String() string
}

var metricBuckets = []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 15, 30, 60, 120, 300, 600}

type MetricsManager interface {
	// StartMetricsEndpoint starts the metrics endpoint at the specified addr/pattern for
	// metrics managed by this MetricsManager. It spawns a goroutine to listen to
	// and serve HTTP requests received on addr/pattern.
	// If the "pattern" is empty (i.e., ""), no endpoint will be started.
	// An error will be returned if there is any.
	StartMetricsEndpoint(pattern, addr string, logger promhttp.Logger, wg *sync.WaitGroup) (*http.Server, error)

	// OperationStart takes in an operation and caches its start time.
	// if the operation already exists, it's an no-op.
	OperationStart(op Operation)

	// DropOperation removes an operation from cache.
	// if the operation does not exist, it's an no-op.
	DropOperation(op Operation)

	// RecordMetrics records a metric point. Note that it will be an no-op if an
	// operation has NOT been marked "Started" previously via invoking "OperationStart".
	// Invoking of RecordMetrics effectively removes the cached entry.
	// op - the operation which the metric is associated with.
	// status - the operation status, if not specified, i.e., status == nil, an
	//          "Unknown" status of the passed-in operation is assumed.
	RecordMetrics(op Operation, status OperationStatus, volumeSnapshot *crdv1.VolumeSnapshot)
}

// Operation is a structure which holds information to identify a snapshot
// related operation
type Operation struct {
	// Name is the name of the operation, for example: "CreateSnapshot", "DeleteSnapshot"
	Name string
	// Driver is the driver name which executes the operation
	Driver string
	// ResourceID is the resource UID to which the operation has been executed against
	ResourceID types.UID
	// SnapshotType represents the snapshot type, for example: "dynamic", "pre-provisioned"
	SnapshotType string
}

// NewOperation initializes a new Operation
func NewOperation(name, driver string, snapshot *crdv1.VolumeSnapshot) Operation {
	snapshotProvisionType := DynamicSnapshotType
	if snapshot.Spec.Source.VolumeSnapshotContentName != nil {
		snapshotProvisionType = PreProvisionedSnapshotType
	}

	return Operation{
		Name:         name,
		Driver:       driver,
		ResourceID:   snapshot.UID,
		SnapshotType: string(snapshotProvisionType),
	}
}

type operationMetricsManager struct {
	// cache is a concurrent-safe map which stores start timestamps for all
	// ongoing operations.
	// key is an Operation
	// value is the timestamp of the start time of the operation
	cache sync.Map

	// registry is a wrapper around Prometheus Registry
	registry k8smetrics.KubeRegistry

	// opReconciliationLatencyMetrics is a Histogram metric for invidual reconciliation metrics
	opReconciliationLatencyMetrics *k8smetrics.HistogramVec

	// opRequestLatencyMetrics is a Histogram metrics for e2e operation time per request
	opRequestLatencyMetrics *k8smetrics.HistogramVec
}

// NewMetricsManager creates a new MetricsManager instance
func NewMetricsManager() MetricsManager {
	mgr := &operationMetricsManager{
		cache: sync.Map{},
	}
	mgr.init()
	return mgr
}

// OperationStart starts a new operation
func (opMgr *operationMetricsManager) OperationStart(op Operation) {
	opMgr.cache.LoadOrStore(op, time.Now())
}

// OperationStart drops an operation
func (opMgr *operationMetricsManager) DropOperation(op Operation) {
	opMgr.cache.Delete(op)
}

// RecordMetrics emits operation metrics
func (opMgr *operationMetricsManager) RecordMetrics(op Operation, status OperationStatus, snapshot *crdv1.VolumeSnapshot) {
	obj, exists := opMgr.cache.Load(op)
	if !exists {
		// the operation has not been cached, return directly
		return
	}
	ts, ok := obj.(time.Time)
	if !ok {
		// the cached item is not a time.Time, should NEVER happen, clean and return
		klog.Errorf("Invalid cache entry for key %v", op)
		opMgr.cache.Delete(op)
		return
	}
	strStatus := string(SnapshotStatusTypeUnknown)
	if status != nil {
		strStatus = status.String()
	}

	// record invidual reconciliation time
	reconciliationDuration := time.Since(ts).Seconds()
	opMgr.opReconciliationLatencyMetrics.WithLabelValues(op.Driver, op.Name, op.SnapshotType, strStatus).Observe(reconciliationDuration)

	// record total operation durations if operation is successful
	if strStatus == string(SnapshotStatusTypeSuccess) {
		switch op.Name {
		case CreateSnapshotAndReadyOperationName:
			// time since user first created volumesnapshot until ReadyToUse is true
			operationDuration := time.Since(snapshot.CreationTimestamp.Time).Seconds()
			opMgr.opRequestLatencyMetrics.WithLabelValues(op.Driver, op.Name, op.SnapshotType, strStatus).Observe(operationDuration)

		case DeleteSnapshotOperationName:
			// check if this is a cancellation. It is a cancellation if the snapshot deletion finished without a status set
			// or there is a status set with ReadyToUse set to false.
			if snapshot.Status == nil || (snapshot.Status.ReadyToUse != nil && !(*snapshot.Status.ReadyToUse)) {
				// get duration from creation time to delete/cancellation
				cancelOperationDuration := time.Since(snapshot.CreationTimestamp.Time).Seconds()

				// emit this metric as a custom cancellation
				opMgr.opRequestLatencyMetrics.WithLabelValues(
					op.Driver,
					CreateSnapshotAndReadyOperationName,
					op.SnapshotType,
					string(SnapshotStatusTypeCancel),
				).Observe(cancelOperationDuration)
			}

			// Still emit the delete operation, as we are both deleting the snapshot and cancelling it's creation.
			// For this duration, we use the time since user marked volumesnapshot for deletion.
			operationDuration := time.Since(snapshot.DeletionTimestamp.Time).Seconds()
			opMgr.opRequestLatencyMetrics.WithLabelValues(op.Driver, op.Name, op.SnapshotType, strStatus).Observe(operationDuration)
		}
	}

	opMgr.cache.Delete(op)
}

func (opMgr *operationMetricsManager) init() {
	opMgr.registry = k8smetrics.NewKubeRegistry()
	opMgr.opReconciliationLatencyMetrics = k8smetrics.NewHistogramVec(
		&k8smetrics.HistogramOpts{
			Subsystem: subSystem,
			Name:      latencyReconciliationMetricName,
			Help:      latencyReconciliationMetricHelpMsg,
			Buckets:   metricBuckets,
		},
		[]string{labelDriverName, labelOperationName, labelSnapshotType, labelOperationStatus},
	)
	opMgr.opRequestLatencyMetrics = k8smetrics.NewHistogramVec(
		&k8smetrics.HistogramOpts{
			Subsystem: subSystem,
			Name:      latencyOperationMetricName,
			Help:      latencyOperationMetricHelpMsg,
			Buckets:   metricBuckets,
		},
		[]string{labelDriverName, labelOperationName, labelSnapshotType, labelOperationStatus},
	)
	opMgr.registry.MustRegister(opMgr.opReconciliationLatencyMetrics)
	opMgr.registry.MustRegister(opMgr.opRequestLatencyMetrics)
}

func (opMgr *operationMetricsManager) StartMetricsEndpoint(pattern, addr string, logger promhttp.Logger, wg *sync.WaitGroup) (*http.Server, error) {
	if addr == "" {
		return nil, fmt.Errorf("metrics endpoint will not be started as endpoint address is not specified")
	}
	// start listening
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on address[%s], error[%v]", addr, err)
	}
	mux := http.NewServeMux()
	mux.Handle(pattern, k8smetrics.HandlerFor(
		opMgr.registry,
		k8smetrics.HandlerOpts{
			ErrorLog:      logger,
			ErrorHandling: k8smetrics.ContinueOnError,
		}))
	srv := &http.Server{Addr: l.Addr().String(), Handler: mux}
	// start serving the endpoint
	go func() {
		defer wg.Done()
		if err := srv.Serve(l); err != http.ErrServerClosed {
			klog.Fatalf("failed to start endpoint at:%s/%s, error: %v", addr, pattern, err)
		}
	}()
	return srv, nil
}

// snapshotProvisionType represents which kind of snapshot a metric is
type snapshotProvisionType string

// snapshotStatusType represents the type of snapshot status to report
type snapshotStatusType string

// SnapshotOperationStatus represents the status for a snapshot controller operation
type SnapshotOperationStatus struct {
	statusCode snapshotStatusType
}

// NewSnapshotOperationStatus returns a new SnapshotOperationStatus
func NewSnapshotOperationStatus(statusCode snapshotStatusType) SnapshotOperationStatus {
	return SnapshotOperationStatus{
		statusCode: statusCode,
	}
}

func (sos SnapshotOperationStatus) String() string {
	return string(sos.statusCode)
}
