package common_controller

import (
	"fmt"
)

const (
	createSnapshotOperationName = "CreateSnapshot"
	snapshottingOperationName   = "Snapshotting"
	deleteSnapshotOperationName = "DeleteSnapshot"

	dynamicSnapshotType        = snapshotType("dynamic")
	preProvisionedSnapshotType = snapshotType("pre-provisioned")
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
