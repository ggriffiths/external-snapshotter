package common_controller

const (
	createSnapshotOperationName = "CreateSnapshot"
	snapshottingOperationName   = "Snapshotting"
	deleteSnapshotOperationName = "DeleteSnapshot"

	dynamicSnapshotType        = snapshotProvisionType("dynamic")
	preProvisionedSnapshotType = snapshotProvisionType("pre-provisioned")

	snapshotStatusTypeSuccess            = snapshotStatusType("success")
	snapshotStatusTypeInvalidRequest     = snapshotStatusType("invalid-request")
	snapshotStatusTypeControllerError    = snapshotStatusType("controller-error")
	snapshotStatusTypeStorageSystemError = snapshotStatusType("storage-system-error")
)

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
