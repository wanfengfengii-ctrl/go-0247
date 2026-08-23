// Package task is the "螺栓检验任务聚合" domain component. It owns the task
// generation, the locked snapshot, the strict status state machine, the per
// node initial/final tightening cursors, the aggregate revision and the
// terminal fence that rejects stale or post-terminal writes.
//
// The persisted entity types live in the shared domain package so the
// persistence layer can round-trip them without an import cycle; this package
// re-exports them and owns the state-machine and sequencing behaviour.
package task

import (
	"boltforge-highstrength-joint-qa/internal/catalog"
	"boltforge-highstrength-joint-qa/internal/domain"
)

// Re-exported persisted entities and enums so callers keep a single import.
type (
	Status         = domain.Status
	TaskNode       = domain.TaskNode
	SamplingRef    = domain.SamplingRef
	TorqueBounds   = domain.TorqueBounds
	RecheckSpec    = domain.RecheckSpec
	BoltSnapshot   = domain.BoltSnapshot
	NodeSnapshot   = domain.NodeSnapshot
	Snapshot       = domain.Snapshot
	InspectionTask = domain.InspectionTask
)

const (
	StatusPendingLock      = domain.StatusPendingLock
	StatusPairVerification = domain.StatusPairVerification
	StatusTorqueRecheck    = domain.StatusTorqueRecheck
	StatusInstallation     = domain.StatusInstallation
	StatusSamplingReview   = domain.StatusSamplingReview
	StatusSignable         = domain.StatusSignable
	StatusSigned           = domain.StatusSigned
	StatusQuarantined      = domain.StatusQuarantined
	StatusCancelled        = domain.StatusCancelled
)

// allowed maps each non-terminal status to the statuses it may transition to.
var allowed = map[Status][]Status{
	StatusPendingLock:      {StatusPairVerification},
	StatusPairVerification: {StatusTorqueRecheck},
	StatusTorqueRecheck:    {StatusInstallation},
	StatusInstallation:     {StatusSamplingReview},
	StatusSamplingReview:   {StatusSignable, StatusQuarantined},
	StatusSignable:         {StatusSigned, StatusQuarantined, StatusCancelled},
	StatusSigned:           {},
	StatusQuarantined:      {},
	StatusCancelled:        {},
}

// CanTransition reports whether the from -> to transition is one of the
// documented adjacent transitions.
func CanTransition(from, to Status) bool {
	for _, t := range allowed[from] {
		if t == to {
			return true
		}
	}
	return false
}

// LockRequest is the input that freezes a task snapshot.
type LockRequest struct {
	TaskID       string             `json:"task_id"`
	Generation   domain.Generation  `json:"generation"`
	OperationNo  domain.OperationNo `json:"operation_no"`
	NodeIDs      []string           `json:"node_ids"`
	SamplingSet  []SamplingRef      `json:"sampling_set"`
	TorqueBounds TorqueBounds       `json:"torque_bounds"`
	RecheckSpec  RecheckSpec        `json:"recheck_spec"`
	DeviceID     string             `json:"device_id"`
	Window       domain.TimeWindow  `json:"window"`
}

// PairVerifyRequest is the connection-pair identity and socket submission.
type PairVerifyRequest struct {
	OperationNo domain.OperationNo `json:"operation_no"`
	OperatorID  string             `json:"operator_id"`
	Revision    domain.Revision    `json:"task_revision"`
	NodeID      string             `json:"node_id"`
	BoltNo      int                `json:"bolt_no"`
	BoltBatch   string             `json:"bolt_batch"`
	NutBatch    string             `json:"nut_batch"`
	WasherBatch string             `json:"washer_batch"`
	Grade       string             `json:"grade"`
	SocketSpec  string             `json:"socket_spec"`
}

// RecheckRequest submits one torque-coefficient recheck trial.
type RecheckRequest struct {
	OperationNo domain.OperationNo `json:"operation_no"`
	OperatorID  string             `json:"operator_id"`
	Revision    domain.Revision    `json:"task_revision"`
	DeviceID    string             `json:"device_id"`
}

// TightenRequest submits one initial or final tightening reading.
type TightenRequest struct {
	OperationNo domain.OperationNo `json:"operation_no"`
	OperatorID  string             `json:"operator_id"`
	Revision    domain.Revision    `json:"task_revision"`
	NodeID      string             `json:"node_id"`
	BoltNo      int                `json:"bolt_no"`
	TorqueNm    domain.TorqueNm    `json:"torque_nm"`
	AngleDeg    domain.AngleDeg    `json:"angle_deg"`
	RecordedAt  int64              `json:"recorded_at_unix"`
	LeaseID     string             `json:"lease_id"`
}

// SamplingRequest submits one sampling recheck conclusion for a sampled bolt.
type SamplingRequest struct {
	OperationNo     domain.OperationNo `json:"operation_no"`
	OperatorID      string             `json:"operator_id"`
	Revision        domain.Revision    `json:"task_revision"`
	NodeID          string             `json:"node_id"`
	BoltNo          int                `json:"bolt_no"`
	MeasuredPreload int                `json:"measured_preload_n"`
	DeviceID        string             `json:"device_id"`
	PersonID        string             `json:"person_id"`
}

// TaskOps is the aggregate port consumed by the HTTP layer.
type TaskOps interface {
	Lock(req LockRequest, cat catalog.Catalog) (*InspectionTask, error)
	Get(taskID string) (*InspectionTask, error)

	VerifyPair(taskID string, rev domain.Revision, v PairVerifyRequest) error
	RecheckTorque(taskID string, rev domain.Revision, r RecheckRequest) error
	TightenInitial(taskID string, rev domain.Revision, r TightenRequest) error
	TightenFinal(taskID string, rev domain.Revision, r TightenRequest) error
	SubmitSampling(taskID string, rev domain.Revision, r SamplingRequest) error
}

// ValidateFinalOrder checks the final-tightening precondition for a node.
func ValidateFinalOrder(node TaskNode, boltNo int) error {
	if boltNo < 1 || boltNo > node.BoltCount {
		return domain.NewError(domain.CodeSequenceGap, "bolt number out of node range")
	}
	if boltNo >= node.InitialCursor {
		return domain.NewError(domain.CodeInvalidPhase, "final tightening before initial tightening")
	}
	if boltNo-1 >= node.FinalCursor {
		return domain.NewError(domain.CodeSequenceGap, "final tightening skips a bolt")
	}
	return nil
}

// ValidateInitialOrder checks the initial-tightening precondition: only the
// bolt at the current cursor may be tightened.
func ValidateInitialOrder(node TaskNode, boltNo int) error {
	if boltNo < 1 || boltNo > node.BoltCount {
		return domain.NewError(domain.CodeSequenceGap, "bolt number out of node range")
	}
	if boltNo != node.InitialCursor {
		return domain.NewError(domain.CodeSequenceGap, "initial tightening must advance contiguously")
	}
	return nil
}

// NewTaskNode builds a TaskNode with both cursors at bolt one.
func NewTaskNode(nodeID string, boltCount int) TaskNode {
	return TaskNode{NodeID: nodeID, BoltCount: boltCount, InitialCursor: 1, FinalCursor: 1}
}
