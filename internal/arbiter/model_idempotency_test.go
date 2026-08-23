package arbiter_test

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"boltforge-highstrength-joint-qa/internal/arbiter"
	"boltforge-highstrength-joint-qa/internal/domain"
	"boltforge-highstrength-joint-qa/internal/store"
)

func modelNewService(t *testing.T) (*store.SQLite, *arbiter.Service) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "model.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, arbiter.NewService(db, domain.NewFakeClock(time.Unix(1700000000, 0)))
}

func modelSeedSignable(t *testing.T, db *store.SQLite, taskID string) {
	t.Helper()
	err := db.WithTx(context.Background(), func(tx store.Tx) error {
		ctx := context.Background()
		if err := tx.SaveTask(ctx, &domain.InspectionTask{
			TaskID: taskID, Generation: 1, Status: domain.StatusSignable,
			Revision: 1, LockSummary: "SUM",
		}, 0); err != nil {
			return err
		}
		if err := tx.PutReview(ctx, domain.Review{
			TaskID: taskID, Seat: domain.SeatFirst, PersonID: "P-ALICE",
			QualificationVersion: "Q-3", TaskSummary: "SUM", EvidenceSummary: "SUM",
			SignedAt: time.Unix(1700000000, 0),
		}); err != nil {
			return err
		}
		return tx.PutReview(ctx, domain.Review{
			TaskID: taskID, Seat: domain.SeatSecond, PersonID: "P-BOB",
			QualificationVersion: "Q-3", TaskSummary: "SUM", EvidenceSummary: "SUM",
			SignedAt: time.Unix(1700000000, 0),
		})
	})
	if err != nil {
		t.Fatalf("seed signable task %s: %v", taskID, err)
	}
}

func TestModel_FinalizeIdempotency(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "identical retry replays decision without duplicate audit",
			run: func(t *testing.T) {
				db, svc := modelNewService(t)
				modelSeedSignable(t, db, "T-1")
				req := arbiter.FinalizeRequest{TaskID: "T-1", OperationNo: "fin-1", OperatorID: "op-a", Type: arbiter.FinalSign}

				first, err := svc.Finalize(req)
				if err != nil {
					t.Fatalf("first finalize: %v", err)
				}
				if first == nil || first.Credential == "" {
					t.Fatalf("first decision = %#v, want credential", first)
				}

				retry, err := svc.Finalize(req)
				if err != nil {
					t.Fatalf("identical retry: %v", err)
				}
				if !reflect.DeepEqual(retry, first) {
					t.Fatalf("retry decision = %#v, want exact replay of %#v", retry, first)
				}

				var finalizeEvents int
				err = db.WithTx(context.Background(), func(tx store.Tx) error {
					events, err := tx.ListAudit(context.Background(), "T-1")
					if err != nil {
						return err
					}
					for _, event := range events {
						if event.Kind == "FINALIZE_SIGN" {
							finalizeEvents++
						}
					}
					return nil
				})
				if err != nil {
					t.Fatalf("read audit: %v", err)
				}
				if finalizeEvents != 1 {
					t.Fatalf("finalize audit events = %d, want 1", finalizeEvents)
				}
			},
		},
		{
			name: "same operation with changed content conflicts",
			run: func(t *testing.T) {
				db, svc := modelNewService(t)
				modelSeedSignable(t, db, "T-1")
				base := arbiter.FinalizeRequest{TaskID: "T-1", OperationNo: "fin-1", OperatorID: "op-a", Type: arbiter.FinalSign}
				if _, err := svc.Finalize(base); err != nil {
					t.Fatalf("first finalize: %v", err)
				}

				_, err := svc.Finalize(arbiter.FinalizeRequest{
					TaskID: "T-1", OperationNo: "fin-1", OperatorID: "op-a", Type: arbiter.FinalCancel,
				})
				if !domain.IsCode(err, domain.CodeIdempotencyConflict) {
					t.Fatalf("changed content error = %v, want IDEMPOTENCY_CONFLICT", err)
				}
			},
		},
		{
			name: "operation numbers are scoped by task",
			run: func(t *testing.T) {
				db, svc := modelNewService(t)
				modelSeedSignable(t, db, "T-1")
				modelSeedSignable(t, db, "T-2")
				for _, taskID := range []string{"T-1", "T-2"} {
					decision, err := svc.Finalize(arbiter.FinalizeRequest{
						TaskID: taskID, OperationNo: "shared-operation", OperatorID: "op-a", Type: arbiter.FinalSign,
					})
					if err != nil {
						t.Fatalf("finalize %s: %v", taskID, err)
					}
					if decision == nil || decision.TaskID != taskID {
						t.Fatalf("decision for %s = %#v", taskID, decision)
					}
					replay, err := svc.Finalize(arbiter.FinalizeRequest{
						TaskID: taskID, OperationNo: "shared-operation", OperatorID: "op-a", Type: arbiter.FinalSign,
					})
					if err != nil || !reflect.DeepEqual(replay, decision) {
						t.Fatalf("replay for %s = %#v, err %v; want %#v", taskID, replay, err, decision)
					}
				}
			},
		},
		{
			name: "different operation remains a stable terminal conflict",
			run: func(t *testing.T) {
				db, svc := modelNewService(t)
				modelSeedSignable(t, db, "T-1")
				if _, err := svc.Finalize(arbiter.FinalizeRequest{
					TaskID: "T-1", OperationNo: "winner", OperatorID: "op-a", Type: arbiter.FinalSign,
				}); err != nil {
					t.Fatalf("winner finalize: %v", err)
				}

				competitor := arbiter.FinalizeRequest{TaskID: "T-1", OperationNo: "competitor", OperatorID: "op-b", Type: arbiter.FinalCancel}
				_, firstErr := svc.Finalize(competitor)
				_, retryErr := svc.Finalize(competitor)
				if !stableTerminalConflict(firstErr) || !stableTerminalConflict(retryErr) {
					t.Fatalf("competitor errors = %v and %v, want stable terminal conflict", firstErr, retryErr)
				}
				if firstErr.(*domain.Error).Code != retryErr.(*domain.Error).Code {
					t.Fatalf("competitor conflict codes differ: %v vs %v", firstErr, retryErr)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
}

func stableTerminalConflict(err error) bool {
	return domain.IsCode(err, domain.CodeFinalizedConflict) || domain.IsCode(err, domain.CodeTerminalState)
}
