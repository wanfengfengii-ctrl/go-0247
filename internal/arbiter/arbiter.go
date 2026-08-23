// Package arbiter is the "复核与终局仲裁器" domain component. It computes the
// node/bolt-sorted reason set, enforces the two-seat independent review and
// runs the single-winner terminal competition among sign, quarantine and
// cancel, emitting exactly one immutable credential.
package arbiter

import (
	"boltforge-highstrength-joint-qa/internal/domain"
)

// Re-exported persisted entities and enums.
type (
	Seat          = domain.Seat
	Review        = domain.Review
	FinalType     = domain.FinalType
	FinalDecision = domain.FinalDecision
)

const (
	SeatUnset  = domain.SeatUnset
	SeatFirst  = domain.SeatFirst
	SeatSecond = domain.SeatSecond

	FinalSign       = domain.FinalSign
	FinalQuarantine = domain.FinalQuarantine
	FinalCancel     = domain.FinalCancel
)

// ReviewRequest is the input to a single review submission.
type ReviewRequest struct {
	TaskID      string             `json:"task_id"`
	OperationNo domain.OperationNo `json:"operation_no"`
	PersonID    string             `json:"person_id"`
	Seat        Seat               `json:"seat"`
	TaskSummary string             `json:"task_summary"`
}

// FinalizeRequest is the input to a terminal competition.
type FinalizeRequest struct {
	TaskID      string             `json:"task_id"`
	OperationNo domain.OperationNo `json:"operation_no"`
	OperatorID  string             `json:"operator_id"`
	Type        FinalType          `json:"type"`
}

// Arbiter is the review/finalization port consumed by the HTTP layer. The
// terminal competition is transactional: only one winner writes the credential
// and terminal state; losers receive a stable FINALIZED_CONFLICT or
// TERMINAL_STATE error.
type Arbiter interface {
	// SubmitReview records one independent review seat. personQualVersion is the
	// submitting person's qualification version resolved from the catalogue;
	// currentQualVersion is the current required version.
	SubmitReview(r ReviewRequest, personQualVersion, currentQualVersion string) error
	Finalize(req FinalizeRequest) (*FinalDecision, error)
}
