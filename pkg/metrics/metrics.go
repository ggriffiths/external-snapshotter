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
	latencyOperationMetricHelpMsg      = "Total number of seconds spent by the controller on an operation from end to end"
	unknownDriverName                  = "unknown"

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

	SnapshotStatusTypeUnknown            snapshotStatusType = "unknown"
	SnapshotStatusTypeInProgress         snapshotStatusType = "in-progress"
	SnapshotStatusTypeInvalidRequest     snapshotStatusType = "invalid-request"
	SnapshotStatusTypeControllerError    snapshotStatusType = "controller-error"
	SnapshotStatusTypeStorageSystemError snapshotStatusType = "storage-system-error"

	// Success and Cancel are statuses for operation time (operation_total_seconds) as seen by snapshot controller
	// SnapshotStatusTypeSuccess represents that a CreateSnapshot, CreateSnapshotAndReady,
	// or DeleteSnapshot has finished successfully.
	// Individual reconciliations (reconciliation_total_seconds) also use this status.
	SnapshotStatusTypeSuccess snapshotStatusType = "success"
	// SnapshotStatusTypeCancel represents that a CreateSnapshot, CreateSnapshotAndReady,
	// or DeleteSnapshot has been deleted before finishing.
	SnapshotStatusTypeCancel snapshotStatusType = "cancel"
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

	// UpdateOperationDriver takes in an operation and updates the cache
	// with a newly discovered driverName, without leaving the old cache record
	UpdateOperationDriver(op Operation, driver string)

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

	if driver == "" {
		driver = unknownDriverName
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

	// opRequestLatencyMetrics is a Histogram metrics for operation time per request
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

// OperationStart starts a new operation
func (opMgr *operationMetricsManager) UpdateOperationDriver(op Operation, driver string) {
	// get ts from existing cache record
	existingTs, ok := opMgr.cache.Load(op)
	if !ok {
		return
	}

	// delete old cache record
	opMgr.cache.Delete(op)

	// update op in cache to have new driver
	op.Driver = driver
	opMgr.cache.Store(op, existingTs)
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

	operationDuration := time.Since(ts).Seconds()
	opMgr.opRequestLatencyMetrics.WithLabelValues(op.Driver, op.Name, op.SnapshotType, strStatus).Observe(operationDuration)

	// Report cancel metrics if we are deleting an unfinished VolumeSnapshot
	if op.Name == DeleteSnapshotOperationName && strStatus == string(SnapshotStatusTypeSuccess) {
		// Check if this is a CreateSnapshot cancellation. It is a cancellation if the snapshot deletion finished without a status set
		// or there is a status with CreationTime as nil.
		if snapshot.Status == nil || (snapshot.Status.CreationTime == nil) {
			cancelOperationDuration := time.Since(ts).Seconds()

			// emit this metric as a custom cancellation
			opMgr.opRequestLatencyMetrics.WithLabelValues(
				op.Driver,
				CreateSnapshotOperationName,
				op.SnapshotType,
				string(SnapshotStatusTypeCancel),
			).Observe(cancelOperationDuration)
		}

		// Check if this is a CreateSnapshotAndReadyOperationName cancellation. It is a cancellation if the snapshot deletion finished without a status set
		// or there is a status set with ReadyToUse set to false.
		if snapshot.Status == nil || (snapshot.Status.ReadyToUse != nil && !(*snapshot.Status.ReadyToUse)) {
			cancelOperationDuration := time.Since(ts).Seconds()

			// emit this metric as a custom cancellation
			opMgr.opRequestLatencyMetrics.WithLabelValues(
				op.Driver,
				CreateSnapshotAndReadyOperationName,
				op.SnapshotType,
				string(SnapshotStatusTypeCancel),
			).Observe(cancelOperationDuration)
		}
	}

	// Only clear from cache on a successful snapshot, otherwise keep
	// reusing to get correct timestamp
	if strStatus == string(SnapshotStatusTypeSuccess) {
		opMgr.cache.Delete(op)
	}
}

func (opMgr *operationMetricsManager) init() {
	opMgr.registry = k8smetrics.NewKubeRegistry()
	opMgr.opRequestLatencyMetrics = k8smetrics.NewHistogramVec(
		&k8smetrics.HistogramOpts{
			Subsystem: subSystem,
			Name:      latencyOperationMetricName,
			Help:      latencyOperationMetricHelpMsg,
			Buckets:   metricBuckets,
		},
		[]string{labelDriverName, labelOperationName, labelSnapshotType, labelOperationStatus},
	)
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
	status snapshotStatusType
}

// NewSnapshotOperationStatus returns a new SnapshotOperationStatus
func NewSnapshotOperationStatus(status snapshotStatusType) SnapshotOperationStatus {
	return SnapshotOperationStatus{
		status: status,
	}
}

func (sos SnapshotOperationStatus) String() string {
	return string(sos.status)
}
