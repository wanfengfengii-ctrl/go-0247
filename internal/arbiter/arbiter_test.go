package arbiter_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"boltforge-highstrength-joint-qa/internal/arbiter"
	"boltforge-highstrength-joint-qa/internal/domain"
	"boltforge-highstrength-joint-qa/internal/store"
)

func newService(t *testing.T) (*store.SQLite, *arbiter.Service) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, arbiter.NewService(s, domain.NewFakeClock(time.Unix(1700000000, 0)))
}

func seedTask(t *testing.T, s *store.SQLite, status domain.Status) {
	t.Helper()
	tk := &domain.InspectionTask{TaskID: "T-1", Generation: 1, Status: status, Revision: 1, LockSummary: "SUM"}
	if err := s.WithTx(context.Background(), func(tx store.Tx) error {
		return tx.SaveTask(context.Background(), tk, 0)
	}); err != nil {
		t.Fatalf("seed task: %v", err)
	}
}

func TestReviewerNotQualified(t *testing.T) {
	s, svc := newService(t)
	seedTask(t, s, domain.StatusSamplingReview)
	err := svc.SubmitReview(arbiter.ReviewRequest{TaskID: "T-1", OperationNo: "rv", PersonID: "P-ALICE", Seat: arbiter.SeatFirst, TaskSummary: "SUM"}, "Q-2", "Q-3")
	if !domain.IsCode(err, domain.CodeReviewerNotQualified) {
		t.Fatalf("want REVIEWER_NOT_QUALIFIED, got %v", err)
	}
}

func TestReviewerOverlap(t *testing.T) {
	s, svc := newService(t)
	seedTask(t, s, domain.StatusSamplingReview)
	if err := svc.SubmitReview(arbiter.ReviewRequest{TaskID: "T-1", OperationNo: "rv1", PersonID: "P-ALICE", Seat: arbiter.SeatFirst, TaskSummary: "SUM"}, "Q-3", "Q-3"); err != nil {
		t.Fatalf("review 1: %v", err)
	}
	err := svc.SubmitReview(arbiter.ReviewRequest{TaskID: "T-1", OperationNo: "rv2", PersonID: "P-ALICE", Seat: arbiter.SeatSecond, TaskSummary: "SUM"}, "Q-3", "Q-3")
	if !domain.IsCode(err, domain.CodeReviewerOverlap) {
		t.Fatalf("want REVIEWER_OVERLAP, got %v", err)
	}
}

func TestSummaryMismatchKeepsReviewOpen(t *testing.T) {
	s, svc := newService(t)
	seedTask(t, s, domain.StatusSamplingReview)
	err := svc.SubmitReview(arbiter.ReviewRequest{TaskID: "T-1", OperationNo: "rv", PersonID: "P-ALICE", Seat: arbiter.SeatFirst, TaskSummary: "WRONG"}, "Q-3", "Q-3")
	if !domain.IsCode(err, domain.CodeSummaryMismatch) {
		t.Fatalf("want SUMMARY_MISMATCH, got %v", err)
	}
}

func TestTerminalCompetitionSingleWinner(t *testing.T) {
	s, svc := newService(t)
	// Seed a signable task with closed sampling (empty set) and two reviews.
	if err := s.WithTx(context.Background(), func(tx store.Tx) error {
		ctx := context.Background()
		if err := tx.SaveTask(ctx, &domain.InspectionTask{TaskID: "T-1", Generation: 1, Status: domain.StatusSignable, Revision: 1, LockSummary: "SUM"}, 0); err != nil {
			return err
		}
		if err := tx.PutReview(ctx, domain.Review{TaskID: "T-1", Seat: domain.SeatFirst, PersonID: "P-ALICE", QualificationVersion: "Q-3", TaskSummary: "SUM", EvidenceSummary: "SUM", SignedAt: time.Unix(1700000000, 0)}); err != nil {
			return err
		}
		return tx.PutReview(ctx, domain.Review{TaskID: "T-1", Seat: domain.SeatSecond, PersonID: "P-BOB", QualificationVersion: "Q-3", TaskSummary: "SUM", EvidenceSummary: "SUM", SignedAt: time.Unix(1700000000, 0)})
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	types := []arbiter.FinalType{arbiter.FinalSign, arbiter.FinalQuarantine, arbiter.FinalCancel}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var wins, losses int
	start := make(chan struct{})
	for _, ft := range types {
		wg.Add(1)
		go func(ft arbiter.FinalType) {
			defer wg.Done()
			<-start
			_, err := svc.Finalize(arbiter.FinalizeRequest{TaskID: "T-1", OperationNo: domain.OperationNo("fin-" + string(ft)), OperatorID: "op", Type: ft})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				wins++
			} else if domain.IsCode(err, domain.CodeFinalizedConflict) || domain.IsCode(err, domain.CodeTerminalState) {
				losses++
			}
		}(ft)
	}
	close(start)
	wg.Wait()

	if wins != 1 || losses != 2 {
		t.Fatalf("wins=%d losses=%d, want 1/2", wins, losses)
	}
}

// seedSignable seeds a signable task with a closed (empty) sampling set and two
// independent qualified reviews, ready for a terminal competition.
func seedSignable(t *testing.T, s *store.SQLite, taskID string) {
	t.Helper()
	if err := s.WithTx(context.Background(), func(tx store.Tx) error {
		ctx := context.Background()
		if err := tx.SaveTask(ctx, &domain.InspectionTask{TaskID: taskID, Generation: 1, Status: domain.StatusSignable, Revision: 1, LockSummary: "SUM"}, 0); err != nil {
			return err
		}
		if err := tx.PutReview(ctx, domain.Review{TaskID: taskID, Seat: domain.SeatFirst, PersonID: "P-ALICE", QualificationVersion: "Q-3", TaskSummary: "SUM", EvidenceSummary: "SUM", SignedAt: time.Unix(1700000000, 0)}); err != nil {
			return err
		}
		return tx.PutReview(ctx, domain.Review{TaskID: taskID, Seat: domain.SeatSecond, PersonID: "P-BOB", QualificationVersion: "Q-3", TaskSummary: "SUM", EvidenceSummary: "SUM", SignedAt: time.Unix(1700000000, 0)})
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

// TestFinalizeReplaysCredentialOnRetry reproduces the field incident: a sign
// finalize succeeds, the client never sees the credential because of a network
// timeout, and it retries with the same operation number. The retry must return
// the original committed credential rather than a TERMINAL_STATE error.
func TestFinalizeReplaysCredentialOnRetry(t *testing.T) {
	s, svc := newService(t)
	seedSignable(t, s, "T-1")

	first, err := svc.Finalize(arbiter.FinalizeRequest{TaskID: "T-1", OperationNo: "fin-1", OperatorID: "op", Type: arbiter.FinalSign})
	if err != nil {
		t.Fatalf("first finalize: %v", err)
	}
	if first.Credential == "" {
		t.Fatal("expected a credential on first finalize")
	}

	// Retry with the same operation number and the same decision type.
	retry, err := svc.Finalize(arbiter.FinalizeRequest{TaskID: "T-1", OperationNo: "fin-1", OperatorID: "op", Type: arbiter.FinalSign})
	if err != nil {
		t.Fatalf("retry finalize should replay, got %v", err)
	}
	if retry.Credential != first.Credential {
		t.Fatalf("retry credential = %q, want replay of %q", retry.Credential, first.Credential)
	}

	// The audit stream must show a single FINALIZE_SIGN, not a duplicate.
	var kinds []string
	if err := s.WithTx(context.Background(), func(tx store.Tx) error {
		events, err := tx.ListAudit(context.Background(), "T-1")
		if err != nil {
			return err
		}
		for _, e := range events {
			kinds = append(kinds, e.Kind)
		}
		return nil
	}); err != nil {
		t.Fatalf("list audit: %v", err)
	}
	var finalizeCount int
	for _, k := range kinds {
		if k == "FINALIZE_SIGN" {
			finalizeCount++
		}
	}
	if finalizeCount != 1 {
		t.Fatalf("FINALIZE_SIGN audit count = %d, want 1 (no duplicate write)", finalizeCount)
	}
}

// TestFinalizeRetryWithDifferentTypeConflicts asserts that reusing the same
// operation number with a different decision type returns
// IDEMPOTENCY_CONFLICT, matching every other mutating operation.
func TestFinalizeRetryWithDifferentTypeConflicts(t *testing.T) {
	s, svc := newService(t)
	seedSignable(t, s, "T-1")

	if _, err := svc.Finalize(arbiter.FinalizeRequest{TaskID: "T-1", OperationNo: "fin-1", OperatorID: "op", Type: arbiter.FinalSign}); err != nil {
		t.Fatalf("first finalize: %v", err)
	}

	// Same operation number, but a different decision type this time.
	_, err := svc.Finalize(arbiter.FinalizeRequest{TaskID: "T-1", OperationNo: "fin-1", OperatorID: "op", Type: arbiter.FinalCancel})
	if !domain.IsCode(err, domain.CodeIdempotencyConflict) {
		t.Fatalf("want IDEMPOTENCY_CONFLICT, got %v", err)
	}
}
