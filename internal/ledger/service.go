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

// ClaimToken binds an available connection token to a bolt. A concurrent claim
// of the same token fails deterministically with TOKEN_BUSY.
func (s *Service) ClaimToken(req ClaimTokenRequest) (*ConnectionToken, error) {
	var out *ConnectionToken
	err := s.store.WithTx(context.Background(), func(tx store.Tx) error {
		ctx := context.Background()
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
