// Package ledger is the "连接副与设备占用账簿" domain component. It issues
// one-shot occupancy tokens for connection pairs and calibration-versioned
// leases for torque devices, and it arbitrates concurrent claims, swaps,
// starts and releases atomically inside a single transaction.
package ledger

import (
	"boltforge-highstrength-joint-qa/internal/domain"
)

// Re-exported persisted entities.
type (
	ConnectionToken = domain.ConnectionToken
	DeviceLease     = domain.DeviceLease
)

// ClaimTokenRequest is the input for claiming a connection-pair token.
type ClaimTokenRequest struct {
	TaskID      string             `json:"task_id"`
	OperationNo domain.OperationNo `json:"operation_no"`
	OperatorID  string             `json:"operator_id"`
	TokenID     string             `json:"token_id"`
	NodeID      string             `json:"node_id"`
	BoltNo      int                `json:"bolt_no"`
}

// ClaimLeaseRequest is the input for claiming a device lease.
type ClaimLeaseRequest struct {
	TaskID             string             `json:"task_id"`
	OperationNo        domain.OperationNo `json:"operation_no"`
	OperatorID         string             `json:"operator_id"`
	DeviceID           string             `json:"device_id"`
	Generation         domain.Generation  `json:"generation"`
	CalibrationVersion string             `json:"calibration_version"`
	HoldSeconds        int64              `json:"hold_seconds"`
}

// Ledger is the occupancy port consumed by the HTTP layer. Every operation is
// atomic: a failed claim or release leaves no partial occupancy.
type Ledger interface {
	ClaimToken(req ClaimTokenRequest) (*ConnectionToken, error)
	ReleaseToken(taskID, tokenID string, operationNo domain.OperationNo) error

	ClaimLease(req ClaimLeaseRequest) (*DeviceLease, error)
	ReleaseLease(taskID, leaseID string, operationNo domain.OperationNo) error
}
