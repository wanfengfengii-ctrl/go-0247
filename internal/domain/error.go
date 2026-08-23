// Package domain holds the shared primitives used across the TorqueChain
// service: stable error codes, the unified rejection value, and the scalar
// domain value types. It is a technical-support package, not one of the six
// core domain components.
package domain

import (
	"encoding/json"
	"sort"
	"strconv"
)

// Code is a stable, machine-readable error code. The set of codes is fixed by
// the project's failure boundaries and is returned verbatim in every rejection
// so that clients and the deterministic test suite can rely on it.
type Code string

const (
	// Catalog / revision mismatches.
	CodeNodeMismatch       Code = "NODE_MISMATCH"
	CodeBatchMismatch      Code = "BATCH_MISMATCH"
	CodePairMismatch       Code = "PAIR_MISMATCH"
	CodeStaleRevision      Code = "STALE_REVISION"
	CodeGenerationMismatch Code = "GENERATION_MISMATCH"

	// Resource contention.
	CodeTokenBusy  Code = "TOKEN_BUSY"
	CodeDeviceBusy Code = "DEVICE_BUSY"

	// Pair compatibility.
	CodeSocketMismatch Code = "SOCKET_MISMATCH"
	CodeGradeMismatch  Code = "GRADE_MISMATCH"
	CodeDuplicatePair  Code = "DUPLICATE_PAIR"
	CodeUnknownPair    Code = "UNKNOWN_PAIR"

	// Sequencing / phase.
	CodeSequenceGap  Code = "SEQUENCE_GAP"
	CodeInvalidPhase Code = "INVALID_PHASE"
	CodeNodeNotFound Code = "NODE_NOT_FOUND"

	// Reading bounds.
	CodeTorqueOutOfRange      Code = "TORQUE_OUT_OF_RANGE"
	CodeAngleOutOfRange       Code = "ANGLE_OUT_OF_RANGE"
	CodeTimeWindowViolation   Code = "TIME_WINDOW_VIOLATION"
	CodeCoefficientOutOfRange Code = "COEFFICIENT_OUT_OF_RANGE"

	// Device failures.
	CodeDeviceRejected     Code = "DEVICE_REJECTED"
	CodeDeviceDisconnected Code = "DEVICE_DISCONNECTED"
	CodeCalibrationExpired Code = "CALIBRATION_EXPIRED"

	// Idempotency.
	CodeIdempotencyConflict Code = "IDEMPOTENCY_CONFLICT"

	// Sampling.
	CodeSampleConflict   Code = "SAMPLE_CONFLICT"
	CodeSampleMissing    Code = "SAMPLE_MISSING"
	CodeSampleOutOfRange Code = "SAMPLE_OUT_OF_RANGE"

	// Review / finalization.
	CodeReviewerNotQualified Code = "REVIEWER_NOT_QUALIFIED"
	CodeReviewerOverlap      Code = "REVIEWER_OVERLAP"
	CodeSummaryMismatch      Code = "SUMMARY_MISMATCH"
	CodeFinalizedConflict    Code = "FINALIZED_CONFLICT"
	CodeTerminalState        Code = "TERMINAL_STATE"
)

// Reason is a single, sortable cause attached to a rejection. The failure
// boundaries require reasons to be ordered by node number and then bolt
// number, so Reason exposes exactly the fields needed to produce that order.
type Reason struct {
	Code Code   `json:"code"`
	Node string `json:"node,omitempty"`
	Bolt int    `json:"bolt,omitempty"`
}

// Error is the unified rejection value. It carries a stable code, the current
// aggregate revision at the time of rejection, and an ordered set of reasons.
type Error struct {
	Code     Code     `json:"code"`
	Message  string   `json:"message"`
	Revision int64    `json:"revision"`
	Reasons  []Reason `json:"reasons,omitempty"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return string(e.Code)
}

// NewError builds a rejection with the given code and message.
func NewError(code Code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// WithRevision sets the aggregate revision carried by the rejection.
func (e *Error) WithRevision(rev int64) *Error {
	e.Revision = rev
	return e
}

// WithReason appends a reason and re-sorts the reason set deterministically.
func (e *Error) WithReason(r Reason) *Error {
	e.Reasons = append(e.Reasons, r)
	e.sortReasons()
	return e
}

// WithReasons replaces the reason set and sorts it deterministically.
func (e *Error) WithReasons(rs []Reason) *Error {
	e.Reasons = append([]Reason(nil), rs...)
	e.sortReasons()
	return e
}

// sortReasons orders reasons by node number (string order) and then by bolt
// number so that repeated executions produce byte-identical output.
func (e *Error) sortReasons() {
	sort.SliceStable(e.Reasons, func(i, j int) bool {
		if e.Reasons[i].Node != e.Reasons[j].Node {
			return e.Reasons[i].Node < e.Reasons[j].Node
		}
		return e.Reasons[i].Bolt < e.Reasons[j].Bolt
	})
}

// SortedReasons returns a copy of the reason set in stable order.
func (e *Error) SortedReasons() []Reason {
	out := append([]Reason(nil), e.Reasons...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Node != out[j].Node {
			return out[i].Node < out[j].Node
		}
		return out[i].Bolt < out[j].Bolt
	})
	return out
}

// Codes returns the reason codes in stable order, used by tests and audit
// summaries for byte-level determinism.
func (e *Error) Codes() []string {
	out := make([]string, 0, len(e.Reasons))
	for _, r := range e.SortedReasons() {
		out = append(out, string(r.Code))
	}
	return out
}

// IsCode reports whether err carries the given stable code.
func IsCode(err error, code Code) bool {
	de, ok := err.(*Error)
	if !ok {
		return false
	}
	return de.Code == code
}

// idempotencyEnvelope is the on-the-wire shape of a persisted idempotency
// result. It carries either a success marker or the original rejection, so an
// identical retry replays the original outcome verbatim instead of silently
// succeeding a pending-retry or out-of-range operation.
type idempotencyEnvelope struct {
	OK    bool   `json:"ok,omitempty"`
	Error *Error `json:"error,omitempty"`
}

// EncodeIdempotencyResult serializes the original operation outcome for the
// idempotency ledger. A nil outcome is a success; a *Error is the rejection
// that the retry must replay.
func EncodeIdempotencyResult(opErr error) string {
	de, _ := opErr.(*Error)
	if de == nil {
		b, _ := json.Marshal(idempotencyEnvelope{OK: true})
		return string(b)
	}
	b, _ := json.Marshal(idempotencyEnvelope{Error: de})
	return string(b)
}

// DecodeIdempotencyResult restores the original operation outcome from the
// idempotency ledger. It returns the rejection the original attempt produced,
// or nil when the original attempt succeeded.
func DecodeIdempotencyResult(result string) error {
	if result == "" {
		return nil
	}
	var env idempotencyEnvelope
	if err := json.Unmarshal([]byte(result), &env); err != nil {
		return nil
	}
	if env.Error != nil {
		// Reasons are persisted in sorted order; rebuild the slice so callers
		// see the same stable, sorted reason set as the original attempt.
		env.Error.sortReasons()
		return env.Error
	}
	return nil
}

// BoltKey builds the canonical "<node>/<bolt>" key used for reason ordering
// and for the sampling / tightening cursor identity.
func BoltKey(node string, bolt int) string {
	return node + "/" + strconv.Itoa(bolt)
}
