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
