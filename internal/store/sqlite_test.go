package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"boltforge-highstrength-joint-qa/internal/domain"
	"boltforge-highstrength-joint-qa/internal/store"
)

func openTest(t *testing.T) *store.SQLite {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestTaskRoundTrip(t *testing.T) {
	s := openTest(t)
	tk := &domain.InspectionTask{
		TaskID: "T-1", Generation: 1, Status: domain.StatusPairVerification, Revision: 1,
		LockSummary: "sum", Nodes: []domain.TaskNode{{NodeID: "N-01", BoltCount: 2, InitialCursor: 1, FinalCursor: 1}},
	}
	if err := s.WithTx(context.Background(), func(tx store.Tx) error {
		return tx.SaveTask(context.Background(), tk, 0)
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	var got *domain.InspectionTask
	if err := s.WithTx(context.Background(), func(tx store.Tx) error {
		var err error
		got, err = tx.LoadTask(context.Background(), "T-1")
		return err
	}); err != nil {
		t.Fatalf("load: %v", err)
	}
	if got == nil || got.TaskID != "T-1" || got.Status != domain.StatusPairVerification {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}

func TestSaveTaskStaleRevisionRejected(t *testing.T) {
	s := openTest(t)
	tk := &domain.InspectionTask{TaskID: "T-1", Generation: 1, Status: domain.StatusPairVerification, Revision: 2}
	if err := s.WithTx(context.Background(), func(tx store.Tx) error {
		return tx.SaveTask(context.Background(), tk, 0)
	}); err != nil {
		t.Fatalf("initial save: %v", err)
	}
	// Saving with a stale expected revision must fail.
	err := s.WithTx(context.Background(), func(tx store.Tx) error {
		tk.Revision = 3
		return tx.SaveTask(context.Background(), tk, 1) // expect revision 1, actual 2
	})
	if !domain.IsCode(err, domain.CodeStaleRevision) {
		t.Fatalf("want STALE_REVISION, got %v", err)
	}
}

func TestTokenClaimAtomicity(t *testing.T) {
	s := openTest(t)
	if err := s.WithTx(context.Background(), func(tx store.Tx) error {
		return tx.InsertToken(context.Background(), domain.ConnectionToken{TokenID: "TOK-1", BatchSummary: "b/n/w", Revision: 1})
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var first, second *domain.ConnectionToken
	var err2 error
	if err := s.WithTx(context.Background(), func(tx store.Tx) error {
		var err error
		first, err = tx.ClaimToken(context.Background(), "TOK-1", "T-1", "N-01", 1, "op")
		return err
	}); err != nil {
		t.Fatalf("claim1: %v", err)
	}
	if first == nil || first.TaskID != "T-1" {
		t.Fatalf("first claim failed: %+v", first)
	}
	err2 = s.WithTx(context.Background(), func(tx store.Tx) error {
		var err error
		second, err = tx.ClaimToken(context.Background(), "TOK-1", "T-2", "N-01", 1, "op2")
		return err
	})
	if second != nil || !domain.IsCode(err2, domain.CodeTokenBusy) {
		t.Fatalf("second claim should be TOKEN_BUSY, got token=%+v err=%v", second, err2)
	}
}

func TestDeviceLeaseAtomicity(t *testing.T) {
	s := openTest(t)
	now := time.Now()
	var err2 error
	if err := s.WithTx(context.Background(), func(tx store.Tx) error {
		_, err := tx.ClaimLease(context.Background(), "LS-1", "TD-001", "T-1", 1, "op", "CAL-1", now.Add(time.Hour), now)
		return err
	}); err != nil {
		t.Fatalf("claim1: %v", err)
	}
	err2 = s.WithTx(context.Background(), func(tx store.Tx) error {
		_, err := tx.ClaimLease(context.Background(), "LS-2", "TD-001", "T-2", 2, "op2", "CAL-1", now.Add(time.Hour), now)
		return err
	})
	if !domain.IsCode(err2, domain.CodeDeviceBusy) {
		t.Fatalf("second lease should be DEVICE_BUSY, got %v", err2)
	}
}

func TestRecoverReclaimsExpiredLeases(t *testing.T) {
	s := openTest(t)
	if err := s.WithTx(context.Background(), func(tx store.Tx) error {
		_, err := tx.ClaimLease(context.Background(), "LS-1", "TD-001", "T-1", 1, "op", "CAL-1", time.Now().Add(-time.Minute), time.Now())
		return err
	}); err != nil {
		t.Fatalf("claim expired lease: %v", err)
	}
	if err := s.Recover(context.Background()); err != nil {
		t.Fatalf("recover: %v", err)
	}
	var released bool
	if err := s.WithTx(context.Background(), func(tx store.Tx) error {
		l, err := tx.GetLease(context.Background(), "LS-1")
		if err != nil {
			return err
		}
		released = l == nil || l.Released
		return nil
	}); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !released {
		t.Fatal("expired lease should be reclaimed by recovery")
	}
}

func TestRestartRecoveryPersistsCommittedData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restart.db")

	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	tk := &domain.InspectionTask{TaskID: "T-1", Generation: 1, Status: domain.StatusPairVerification, Revision: 1, LockSummary: "sum"}
	if err := s.WithTx(context.Background(), func(tx store.Tx) error {
		return tx.SaveTask(context.Background(), tk, 0)
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := s.WithTx(context.Background(), func(tx store.Tx) error {
		return tx.AppendAudit(context.Background(), store.AuditEvent{TaskID: "T-1", Operation: "op", Kind: "LOCK", ContentDigest: "d", Committed: true})
	}); err != nil {
		t.Fatalf("audit: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopen the same file and verify the committed data survived.
	s2, err := store.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	var got *domain.InspectionTask
	if err := s2.WithTx(context.Background(), func(tx store.Tx) error {
		var err error
		got, err = tx.LoadTask(context.Background(), "T-1")
		return err
	}); err != nil {
		t.Fatalf("load after reopen: %v", err)
	}
	if got == nil || got.TaskID != "T-1" {
		t.Fatalf("committed task missing after restart: %+v", got)
	}
	var audit []store.AuditEvent
	if err := s2.WithTx(context.Background(), func(tx store.Tx) error {
		var err error
		audit, err = tx.ListAudit(context.Background(), "T-1")
		return err
	}); err != nil {
		t.Fatalf("audit after reopen: %v", err)
	}
	if len(audit) != 1 {
		t.Fatalf("audit events = %d, want 1", len(audit))
	}
}
