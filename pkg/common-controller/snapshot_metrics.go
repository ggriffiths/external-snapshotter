package common_controller

import (
	"fmt"

	crdv1 "github.com/kubernetes-csi/external-snapshotter/client/v3/apis/volumesnapshot/v1beta1"
	"github.com/kubernetes-csi/external-snapshotter/v3/pkg/metrics"
	"github.com/kubernetes-csi/external-snapshotter/v3/pkg/utils"
	klog "k8s.io/klog/v2"
)

const (
	createSnapshotOperationName = "CreateSnapshot"
	snapshottingOperationName   = "Snapshotting"
	deleteSnapshotOperationName = "DeleteSnapshot"

	dynamicSnapshotType        = "dynamic"
	preProvisionedSnapshotType = "pre-provisioned"
)

// snapshotKind represents which kind of snapshot a metric is
type snapshotType string

// SnapshotOperationStatus represents the status for a snapshot controller operation
type SnapshotOperationStatus struct {
	Status string
	Error  string
}

// NewSnapshotOperationStatus returns a new SnapshotOperationStatus
func NewSnapshotOperationStatus(err error) SnapshotOperationStatus {
	if err != nil {
		return SnapshotOperationStatus{
			Status: "failed",
			Error:  err.Error(),
		}
	}

	return SnapshotOperationStatus{
		Status: "success",
	}
}

func (sos SnapshotOperationStatus) String() string {
	if sos.Error != "" {
		return fmt.Sprintf("%s: %s", sos.Status, sos.Error)
	}

	// no error, so operation was successful
	return "success"
}

func (ctrl *csiSnapshotCommonController) StartMetricsOperation(snapshot *crdv1.VolumeSnapshot, operationName string, snapshotType snapshotType) {
	driverName, err := ctrl.getSnapshotDriverName(snapshot)
	if err != nil {
		klog.Errorf("failed to getSnapshotDriverName while starting metrics operation for snapshot %q: %s", utils.SnapshotKey(snapshot), err)
	}
	ctrl.metricsManager.OperationStart(metrics.Operation{
		Name:         operationName,
		Driver:       driverName,
		ResourceID:   snapshot.UID,
		SnapshotType: string(snapshotType),
	})
}

func (ctrl *csiSnapshotCommonController) RecordSnapshotMetrics(snapshot *crdv1.VolumeSnapshot, operationName string, snapshotType string, err error) {
	driverName, err := ctrl.getSnapshotDriverName(snapshot)
	if err != nil {
		klog.Errorf("failed to getSnapshotDriverName while recording %s metrics for snapshot %q: %s", operationName, utils.SnapshotKey(snapshot), err)
	}
	ctrl.metricsManager.RecordMetrics(metrics.Operation{
		Name:         operationName,
		Driver:       driverName,
		ResourceID:   snapshot.UID,
		SnapshotType: snapshotType,
	}, NewSnapshotOperationStatus(err))
}
