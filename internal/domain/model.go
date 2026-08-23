package domain

import "time"

// Status is the strict task lifecycle state (see the task package for the
// transition table). It lives here so the persistence layer can round-trip it
// without importing the task aggregate.
type Status string

const (
	StatusPendingLock      Status = "PENDING_LOCK"
	StatusPairVerification Status = "PAIR_VERIFICATION"
	StatusTorqueRecheck    Status = "TORQUE_RECHECK"
	StatusInstallation     Status = "INSTALLATION_TIGHTENING"
	StatusSamplingReview   Status = "SAMPLING_REVIEW"
	StatusSignable         Status = "SIGNABLE"
	StatusSigned           Status = "SIGNED"
	StatusQuarantined      Status = "QUARANTINED"
	StatusCancelled        Status = "CANCELLED"
)

// IsTerminal reports whether s is a terminal status that can never change.
func (s Status) IsTerminal() bool {
	return s == StatusSigned || s == StatusQuarantined || s == StatusCancelled
}

// Next returns the single forward transition for the happy-path statuses, or
// ok=false when s has no single forward edge.
func (s Status) Next() (Status, bool) {
	switch s {
	case StatusPendingLock:
		return StatusPairVerification, true
	case StatusPairVerification:
		return StatusTorqueRecheck, true
	case StatusTorqueRecheck:
		return StatusInstallation, true
	case StatusInstallation:
		return StatusSamplingReview, true
	case StatusSamplingReview:
		return StatusSignable, true
	case StatusSignable:
		return StatusSigned, true
	default:
		return "", false
	}
}

// TaskNode holds the per-node tightening cursors.
type TaskNode struct {
	NodeID        string `json:"node_id"`
	BoltCount     int    `json:"bolt_count"`
	InitialCursor int    `json:"initial_cursor"`
	FinalCursor   int    `json:"final_cursor"`
}

// SamplingRef identifies a bolt in the fixed sampling set.
type SamplingRef struct {
	NodeID string `json:"node_id"`
	BoltNo int    `json:"bolt_no"`
}

// TorqueBounds is the design preload and allowed torque/angle intervals.
type TorqueBounds struct {
	DesignPreload int   `json:"design_preload_n"`
	TorqueRange   Range `json:"torque_range_nm"`
	AngleRange    Range `json:"angle_range_deg"`
}

// RecheckSpec fixes the torque-coefficient recheck requirements at lock time.
type RecheckSpec struct {
	Trials         int     `json:"trials"`
	CoefficientMin float64 `json:"coefficient_min"`
	CoefficientMax float64 `json:"coefficient_max"`
}

// BoltSnapshot freezes one bolt's grade, socket and resolved batch triple.
type BoltSnapshot struct {
	BoltNo       int      `json:"bolt_no"`
	Grade        string   `json:"grade"`
	SocketSpec   string   `json:"socket_spec"`
	BoltBatch    string   `json:"bolt_batch"`
	NutBatch     string   `json:"nut_batch"`
	WasherBatch  string   `json:"washer_batch"`
	RuleID       string   `json:"rule_id"`
	RuleRevision Revision `json:"rule_revision"`
}

// NodeSnapshot freezes one node's geometry and ordered bolt list.
type NodeSnapshot struct {
	NodeID          string         `json:"node_id"`
	GeometrySummary string         `json:"geometry_summary"`
	Revision        Revision       `json:"revision"`
	Bolts           []BoltSnapshot `json:"bolts"`
}

// Snapshot is the immutable lock-time freeze of every catalogue input.
type Snapshot struct {
	Project           string         `json:"project"`
	Nodes             []NodeSnapshot `json:"nodes"`
	DeviceID          string         `json:"device_id"`
	DeviceCalVersion  string         `json:"device_cal_version"`
	DeviceTorqueRange Range          `json:"device_torque_range"`
	DeviceAngleRange  Range          `json:"device_angle_range"`
	CatalogRevision   Revision       `json:"catalog_revision"`
	TorqueBounds      TorqueBounds   `json:"torque_bounds"`
	RecheckSpec       RecheckSpec    `json:"recheck_spec"`
	Window            TimeWindow     `json:"window"`
}

// InspectionTask is the task aggregate root.
type InspectionTask struct {
	TaskID           string        `json:"task_id"`
	Generation       Generation    `json:"generation"`
	LockSummary      string        `json:"lock_summary"`
	Status           Status        `json:"status"`
	Revision         Revision      `json:"revision"`
	Nodes            []TaskNode    `json:"nodes"`
	SamplingSet      []SamplingRef `json:"sampling_set"`
	TorqueBounds     TorqueBounds  `json:"torque_bounds"`
	RecheckSpec      RecheckSpec   `json:"recheck_spec"`
	DeviceCalVersion string        `json:"device_calibration_version"`
	Window           TimeWindow    `json:"window"`
	Snapshot         Snapshot      `json:"snapshot"`
	Credential       string        `json:"credential,omitempty"`
}

// ConnectionToken is a one-shot occupancy token for a connection pair.
type ConnectionToken struct {
	TokenID      string   `json:"token_id"`
	TaskID       string   `json:"task_id"`
	NodeID       string   `json:"node_id"`
	BoltNo       int      `json:"bolt_no"`
	BatchSummary string   `json:"batch_summary"`
	Consumed     bool     `json:"consumed"`
	Holder       string   `json:"holder"`
	Released     bool     `json:"released"`
	Revision     Revision `json:"revision"`
}

// DeviceLease is a calibration-versioned lease on a torque device.
type DeviceLease struct {
	LeaseID            string     `json:"lease_id"`
	DeviceID           string     `json:"device_id"`
	TaskID             string     `json:"task_id"`
	TaskGeneration     Generation `json:"task_generation"`
	Holder             string     `json:"holder"`
	CalibrationVersion string     `json:"calibration_version"`
	Revision           Revision   `json:"revision"`
	ExpiresAt          time.Time  `json:"expires_at"`
	Released           bool       `json:"released"`
}

// IsExpired reports whether the lease has passed its expiry.
func (l DeviceLease) IsExpired(now time.Time) bool { return now.After(l.ExpiresAt) }

// Phase distinguishes the initial and final tightening stages.
type Phase string

const (
	PhaseInitial Phase = "INITIAL"
	PhaseFinal   Phase = "FINAL"
)

// SampleResult is the single valid conclusion of a sampling recheck.
type SampleResult string

const (
	SampleOK      SampleResult = "OK"
	SampleLow     SampleResult = "LOW"
	SampleHigh    SampleResult = "HIGH"
	SamplePending SampleResult = "PENDING_RETRY"
)

// PairVerification records a connection-pair identity and socket submission.
type PairVerification struct {
	OperationNo OperationNo `json:"operation_no"`
	TaskID      string      `json:"task_id"`
	NodeID      string      `json:"node_id"`
	BoltNo      int         `json:"bolt_no"`
	BoltBatch   string      `json:"bolt_batch"`
	NutBatch    string      `json:"nut_batch"`
	WasherBatch string      `json:"washer_batch"`
	Grade       string      `json:"grade"`
	SocketSpec  string      `json:"socket_spec"`
	Result      string      `json:"result"`
}

// TorqueCoefficientRun records one recheck trial against the scripted device.
type TorqueCoefficientRun struct {
	OperationNo        OperationNo `json:"operation_no"`
	TaskID             string      `json:"task_id"`
	RetrySeq           int         `json:"retry_seq"`
	DeviceID           string      `json:"device_id"`
	CalibrationVersion string      `json:"calibration_version"`
	Coefficient        float64     `json:"coefficient"`
	DeviceReceipt      string      `json:"device_receipt"`
	Result             string      `json:"result"`
}

// TighteningRecord is an immutable initial or final tightening entry.
type TighteningRecord struct {
	OperationNo  OperationNo `json:"operation_no"`
	TaskID       string      `json:"task_id"`
	Phase        Phase       `json:"phase"`
	NodeID       string      `json:"node_id"`
	BoltNo       int         `json:"bolt_no"`
	TorqueNm     TorqueNm    `json:"torque_nm"`
	AngleDeg     AngleDeg    `json:"angle_deg"`
	RecordedAt   time.Time   `json:"recorded_at"`
	LeaseID      string      `json:"lease_id"`
	CursorBefore int         `json:"cursor_before"`
	CursorAfter  int         `json:"cursor_after"`
	Summary      string      `json:"summary"`
}

// SamplingResult is one immutable entry in the sampling chain.
type SamplingResult struct {
	OperationNo     OperationNo  `json:"operation_no"`
	TaskID          string       `json:"task_id"`
	NodeID          string       `json:"node_id"`
	BoltNo          int          `json:"bolt_no"`
	AttemptSeq      int          `json:"attempt_seq"`
	MeasuredPreload int          `json:"measured_preload_n"`
	AllowedMin      int          `json:"allowed_min_n"`
	AllowedMax      int          `json:"allowed_max_n"`
	DeviceID        string       `json:"device_id"`
	PersonID        string       `json:"person_id"`
	Result          SampleResult `json:"result"`
	EvidenceSummary string       `json:"evidence_summary"`
	PrevHash        string       `json:"prev_hash"`
}

// Seat identifies one of the two independent review positions.
type Seat int

const (
	SeatUnset Seat = iota
	SeatFirst
	SeatSecond
)

// Review is one independent review submission.
type Review struct {
	TaskID               string    `json:"task_id"`
	Seat                 Seat      `json:"seat"`
	PersonID             string    `json:"person_id"`
	QualificationVersion string    `json:"qualification_version"`
	TaskSummary          string    `json:"task_summary"`
	EvidenceSummary      string    `json:"evidence_summary"`
	SignedAt             time.Time `json:"signed_at"`
}

// FinalType is the single winning terminal decision kind.
type FinalType string

const (
	FinalSign       FinalType = "SIGN"
	FinalQuarantine FinalType = "QUARANTINE"
	FinalCancel     FinalType = "CANCEL"
)

// FinalDecision is the unique immutable terminal credential.
type FinalDecision struct {
	TaskID           string      `json:"task_id"`
	Type             FinalType   `json:"type"`
	WinningOperation OperationNo `json:"winning_operation_no"`
	ReasonSummary    string      `json:"reason_summary"`
	Credential       string      `json:"credential"`
	SubmittedAt      time.Time   `json:"submitted_at"`
}

// WithCredential returns a copy of the decision with its terminal credential
// derived from the frozen decision fields. The digest is taken before the
// credential is set, so it is a pure function of TaskID, Type,
// WinningOperation, ReasonSummary and SubmittedAt. Sharing this derivation
// keeps the arbiter competition and the sampling-failure quarantine path
// byte-identical for the same decision.
func (d FinalDecision) WithCredential() FinalDecision {
	d.Credential = "CRED-" + Digest(d)[:16]
	return d
}

// ReasonSummary returns the canonical, type-specific reason summary used by
// both the arbiter competition and the sampling-failure quarantine path, so a
// quarantine recorded either way carries the same summary text.
func ReasonSummary(t FinalType) string {
	switch t {
	case FinalSign:
		return "all nodes passed, sampling closed, dual review complete"
	case FinalQuarantine:
		return "sampling or review failure isolated for rework"
	default:
		return "task cancelled"
	}
}
