// Package records is the "初拧终拧及抽检记录账簿" domain component. It stores
// append-only, non-overwritable evidence: pair verifications, torque
// coefficient runs, initial/final tightening records, device receipts and the
// sampling result chain, each keyed by an idempotent operation number.
package records

import (
	"boltforge-highstrength-joint-qa/internal/domain"
)

// Re-exported persisted entities and enums.
type (
	Phase                = domain.Phase
	SampleResult         = domain.SampleResult
	PairVerification     = domain.PairVerification
	TorqueCoefficientRun = domain.TorqueCoefficientRun
	TighteningRecord     = domain.TighteningRecord
	SamplingResult       = domain.SamplingResult
)

const (
	PhaseInitial = domain.PhaseInitial
	PhaseFinal   = domain.PhaseFinal

	SampleOK      = domain.SampleOK
	SampleLow     = domain.SampleLow
	SampleHigh    = domain.SampleHigh
	SamplePending = domain.SamplePending
)

// Digest computes the canonical content digest of a value, used for idempotency
// comparison and evidence summaries.
func Digest(v any) string { return domain.Digest(v) }

// ChainHash links one sampling result to its predecessor's digest.
func ChainHash(prev string, r SamplingResult) string {
	return domain.DigestPair(prev, r.EvidenceSummary)
}

// Records is the evidence-ledger read port consumed by the HTTP layer and the
// arbiter. It projects the append-only evidence stream for a task.
type Records interface {
	PairVerifications(taskID string) ([]PairVerification, error)
	TorqueRechecks(taskID string) ([]TorqueCoefficientRun, error)
	Tightenings(taskID string) ([]TighteningRecord, error)
	SamplingResults(taskID string) ([]SamplingResult, error)
}
