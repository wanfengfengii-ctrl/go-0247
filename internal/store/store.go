// Package store is the technical persistence support package. It provides a
// transactional SQLite-WAL engine, the append-only audit stream, the
// idempotency ledger and the schema/recovery metadata. Every domain write runs
// inside a transaction so a failure leaves no partial token, device lease,
// tightening record, sampling result or status revision.
//
// The domain packages depend only on the Store port defined here, so the
// in-memory reference and the SQLite backend are interchangeable and the
// aggregate types round-trip through the shared domain package.
package store

import (
	"context"
	"time"

	"boltforge-highstrength-joint-qa/internal/domain"
)

// SchemaVersion records the applied schema revision and WAL recovery metadata.
type SchemaVersion struct {
	Version       int   `json:"version"`
	AppliedAtSeq  int64 `json:"applied_at_seq"`
	LastCommitSeq int64 `json:"last_commit_seq"`
}

// AuditEvent is an append-only audit entry with a monotonic sequence number and
// a transaction commit marker.
type AuditEvent struct {
	Seq           int64              `json:"seq"`
	TaskID        string             `json:"task_id"`
	Operation     domain.OperationNo `json:"operation_no"`
	Kind          string             `json:"kind"`
	ContentDigest string             `json:"content_digest"`
	Committed     bool               `json:"committed"`
}

// IdempotencyEntry stores the normalized request digest and the original
// result so an identical retry can be replayed without a second write.
type IdempotencyEntry struct {
	OperationNo domain.OperationNo `json:"operation_no"`
	TaskID      string             `json:"task_id"`
	Digest      string             `json:"digest"`
	Result      string             `json:"result"`
}

// Tx is a single atomic unit of work. Nothing is published to readers until
// Commit succeeds, and every write is reachable through the typed methods.
type Tx interface {
	// Tasks.
	LoadTask(ctx context.Context, taskID string) (*domain.InspectionTask, error)
	SaveTask(ctx context.Context, t *domain.InspectionTask, expectRevision domain.Revision) error

	// Append-only evidence.
	AppendPairVerification(ctx context.Context, v domain.PairVerification) error
	AppendTorqueRecheck(ctx context.Context, r domain.TorqueCoefficientRun) error
	AppendTightening(ctx context.Context, r domain.TighteningRecord) error
	AppendSamplingResult(ctx context.Context, r domain.SamplingResult) error

	ListPairVerifications(ctx context.Context, taskID string) ([]domain.PairVerification, error)
	ListTorqueRechecks(ctx context.Context, taskID string) ([]domain.TorqueCoefficientRun, error)
	ListTightenings(ctx context.Context, taskID string) ([]domain.TighteningRecord, error)
	ListSamplingResults(ctx context.Context, taskID string) ([]domain.SamplingResult, error)

	// Connection tokens.
	InsertToken(ctx context.Context, t domain.ConnectionToken) error
	ClaimToken(ctx context.Context, tokenID, taskID, nodeID string, boltNo int, holder string) (*domain.ConnectionToken, error)
	ClaimAvailableToken(ctx context.Context, taskID, nodeID string, boltNo int, holder string) (*domain.ConnectionToken, error)
	ConsumeToken(ctx context.Context, taskID, nodeID string, boltNo int) error
	ReleaseToken(ctx context.Context, taskID, tokenID string) error

	// Device leases.
	InsertLease(ctx context.Context, l domain.DeviceLease) error
	ClaimLease(ctx context.Context, leaseID, deviceID, taskID string, generation domain.Generation, holder, calVersion string, expiresAt time.Time, now time.Time) (*domain.DeviceLease, error)
	ReleaseLease(ctx context.Context, taskID, leaseID string) error
	GetLease(ctx context.Context, leaseID string) (*domain.DeviceLease, error)
	ListLeases(ctx context.Context, taskID string) ([]domain.DeviceLease, error)
	ReclaimExpiredLeases(ctx context.Context, now time.Time) (int, error)

	// Reviews and terminal decisions.
	PutReview(ctx context.Context, r domain.Review) error
	ListReviews(ctx context.Context, taskID string) ([]domain.Review, error)
	PutDecision(ctx context.Context, d domain.FinalDecision) error
	GetDecision(ctx context.Context, taskID string) (*domain.FinalDecision, error)

	// Idempotency and audit.
	GetIdempotency(ctx context.Context, op domain.OperationNo, taskID string) (IdempotencyEntry, bool, error)
	PutIdempotency(ctx context.Context, e IdempotencyEntry) error
	AppendAudit(ctx context.Context, e AuditEvent) error
	ListAudit(ctx context.Context, taskID string) ([]AuditEvent, error)

	LoadSchemaVersion(ctx context.Context) (SchemaVersion, error)
	SaveSchemaVersion(ctx context.Context, v SchemaVersion) error
}

// Store is the transactional persistence port consumed by the domain services.
type Store interface {
	// WithTx runs fn inside a single transaction, committing on success and
	// rolling back on any error.
	WithTx(ctx context.Context, fn func(Tx) error) error
	// Recover performs startup recovery: it verifies the WAL is consistent,
	// reclaims expired unconsumed leases and re-establishes the schema version.
	Recover(ctx context.Context) error
	// Close releases the underlying database.
	Close() error
	// Path returns the on-disk database path.
	Path() string
}
