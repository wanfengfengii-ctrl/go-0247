package ledger

import (
	"context"
	"time"

	"boltforge-highstrength-joint-qa/internal/domain"
	"boltforge-highstrength-joint-qa/internal/store"
)

// Service is the concrete occupancy ledger. Token and lease claims use
// conditional updates inside a transaction so concurrent claims are decided
// atomically and a rollback leaves no partial occupancy.
type Service struct {
	store store.Store
	clock domain.Clock
}

// NewService wires the ledger against the persistence and clock ports.
func NewService(st store.Store, clock domain.Clock) *Service {
	return &Service{store: st, clock: clock}
}

// requireOpenTask loads the task targeted by a resource claim and enforces the
// existence and terminal-state fences inside the claim transaction, so a token
// or lease can never be bound to a missing, already-signed, quarantined or
// cancelled task. Device-lease callers additionally check the declared
// generation against the loaded task.
func requireOpenTask(ctx context.Context, tx store.Tx, taskID string) (*domain.InspectionTask, error) {
	t, err := tx.LoadTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, domain.NewError(domain.CodeNodeNotFound, "task not found")
	}
	if t.Status.IsTerminal() {
		return nil, domain.NewError(domain.CodeTerminalState, "task is in a terminal state")
	}
	return t, nil
}

// ClaimToken binds an available connection token to a bolt. The target task must
// exist and be open; a concurrent claim of the same token fails
// deterministically with TOKEN_BUSY.
func (s *Service) ClaimToken(req ClaimTokenRequest) (*ConnectionToken, error) {
	var out *ConnectionToken
	err := s.store.WithTx(context.Background(), func(tx store.Tx) error {
		ctx := context.Background()
		if _, err := requireOpenTask(ctx, tx, req.TaskID); err != nil {
			return err
		}
		tok, err := tx.ClaimToken(ctx, req.TokenID, req.TaskID, req.NodeID, req.BoltNo, req.OperatorID)
		if err != nil {
			return err
		}
		tx.AppendAudit(ctx, store.AuditEvent{TaskID: req.TaskID, Operation: req.OperationNo, Kind: "CLAIM_TOKEN", ContentDigest: string(req.TokenID), Committed: true})
		out = tok
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ReleaseToken releases an unconsumed token held by the task.
func (s *Service) ReleaseToken(taskID, tokenID string, operationNo domain.OperationNo) error {
	return s.store.WithTx(context.Background(), func(tx store.Tx) error {
		ctx := context.Background()
		if err := tx.ReleaseToken(ctx, taskID, tokenID); err != nil {
			return err
		}
		tx.AppendAudit(ctx, store.AuditEvent{TaskID: taskID, Operation: operationNo, Kind: "RELEASE_TOKEN", ContentDigest: tokenID, Committed: true})
		return nil
	})
}

// ClaimLease atomically leases a torque device for the task with a calibration
// version and expiry. Only one active lease per device is allowed.
func (s *Service) ClaimLease(req ClaimLeaseRequest) (*DeviceLease, error) {
	if req.HoldSeconds <= 0 {
		req.HoldSeconds = 3600
	}
	leaseID := "LS-" + domain.Digest(req)[:12]
	expiresAt := s.clock.Now().Add(time.Duration(req.HoldSeconds) * time.Second)
	var out *DeviceLease
	err := s.store.WithTx(context.Background(), func(tx store.Tx) error {
		ctx := context.Background()
		// The target task must exist, be open and match the declared generation.
		// Without this fence a claim against a signed/quarantined/cancelled task
		// or a mistyped task id still succeeds and locks the device away from the
		// genuinely open task.
		t, err := requireOpenTask(ctx, tx, req.TaskID)
		if err != nil {
			return err
		}
		if t.Generation != req.Generation {
			return domain.NewError(domain.CodeGenerationMismatch, "task generation mismatch")
		}
		l, err := tx.ClaimLease(ctx, leaseID, req.DeviceID, req.TaskID, req.Generation, req.OperatorID, req.CalibrationVersion, expiresAt, s.clock.Now())
		if err != nil {
			return err
		}
		tx.AppendAudit(ctx, store.AuditEvent{TaskID: req.TaskID, Operation: req.OperationNo, Kind: "CLAIM_LEASE", ContentDigest: req.DeviceID, Committed: true})
		out = l
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ReleaseLease releases a device lease held by the task.
func (s *Service) ReleaseLease(taskID, leaseID string, operationNo domain.OperationNo) error {
	return s.store.WithTx(context.Background(), func(tx store.Tx) error {
		ctx := context.Background()
		if err := tx.ReleaseLease(ctx, taskID, leaseID); err != nil {
			return err
		}
		tx.AppendAudit(ctx, store.AuditEvent{TaskID: taskID, Operation: operationNo, Kind: "RELEASE_LEASE", ContentDigest: leaseID, Committed: true})
		return nil
	})
}

// SeedTokens inserts a pool of connection tokens into the global pool, used by
// the executable bootstrap and by the deterministic tests.
func SeedTokens(ctx context.Context, st store.Store, tokens []domain.ConnectionToken) error {
	return st.WithTx(ctx, func(tx store.Tx) error {
		for _, tok := range tokens {
			if err := tx.InsertToken(ctx, tok); err != nil {
				return err
			}
		}
		return nil
	})
}
