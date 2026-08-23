package arbiter_test

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"boltforge-highstrength-joint-qa/internal/arbiter"
	"boltforge-highstrength-joint-qa/internal/domain"
	"boltforge-highstrength-joint-qa/internal/ledger"
	"boltforge-highstrength-joint-qa/internal/store"
)

func TestModel_ArbiterSignRespectsLeaseState(t *testing.T) {
	tests := []struct {
		name        string
		release     bool
		expire      bool
		wantBlocked bool
	}{
		{name: "active lease blocks sign without side effects", wantBlocked: true},
		{name: "released lease permits sign", release: true},
		{name: "expired reclaimed lease permits sign", expire: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			clock := domain.NewFakeClock(time.Unix(1_700_000_000, 0))
			st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatalf("open store: %v", err)
			}
			t.Cleanup(func() { _ = st.Close() })

			if err := st.WithTx(ctx, func(tx store.Tx) error {
				return tx.SaveTask(ctx, &domain.InspectionTask{
					TaskID:      "T-LEASE",
					Generation:  1,
					LockSummary: "LOCK-SUMMARY",
					Status:      domain.StatusSamplingReview,
					Revision:    1,
				}, 0)
			}); err != nil {
				t.Fatalf("seed task: %v", err)
			}

			leaseService := ledger.NewService(st, clock)
			lease, err := leaseService.ClaimLease(ledger.ClaimLeaseRequest{
				TaskID:             "T-LEASE",
				OperationNo:        "claim-lease",
				OperatorID:         "installer",
				DeviceID:           "TD-ACTIVE",
				Generation:         1,
				CalibrationVersion: "CAL-1",
				HoldSeconds:        60,
			})
			if err != nil {
				t.Fatalf("claim lease: %v", err)
			}

			service := arbiter.NewService(st, clock)
			for i, review := range []arbiter.ReviewRequest{
				{TaskID: "T-LEASE", OperationNo: "review-1", PersonID: "P-ALICE", Seat: arbiter.SeatFirst, TaskSummary: "LOCK-SUMMARY"},
				{TaskID: "T-LEASE", OperationNo: "review-2", PersonID: "P-BOB", Seat: arbiter.SeatSecond, TaskSummary: "LOCK-SUMMARY"},
			} {
				if err := service.SubmitReview(review, "Q-CURRENT", "Q-CURRENT"); err != nil {
					t.Fatalf("submit review %d: %v", i+1, err)
				}
			}

			if tt.release {
				if err := leaseService.ReleaseLease("T-LEASE", lease.LeaseID, "release-lease"); err != nil {
					t.Fatalf("release lease: %v", err)
				}
			}
			if tt.expire {
				clock.Advance(61 * time.Second)
				if err := st.WithTx(ctx, func(tx store.Tx) error {
					reclaimed, err := tx.ReclaimExpiredLeases(ctx, clock.Now())
					if err == nil && reclaimed != 1 {
						t.Fatalf("reclaimed leases = %d, want 1", reclaimed)
					}
					return err
				}); err != nil {
					t.Fatalf("reclaim expired lease: %v", err)
				}
			}

			type snapshot struct {
				task     *domain.InspectionTask
				decision *domain.FinalDecision
				leases   []domain.DeviceLease
				audit    []store.AuditEvent
			}
			readSnapshot := func() snapshot {
				t.Helper()
				var got snapshot
				if err := st.WithTx(ctx, func(tx store.Tx) error {
					var err error
					if got.task, err = tx.LoadTask(ctx, "T-LEASE"); err != nil {
						return err
					}
					if got.decision, err = tx.GetDecision(ctx, "T-LEASE"); err != nil {
						return err
					}
					if got.leases, err = tx.ListLeases(ctx, "T-LEASE"); err != nil {
						return err
					}
					got.audit, err = tx.ListAudit(ctx, "T-LEASE")
					return err
				}); err != nil {
					t.Fatalf("read state: %v", err)
				}
				return got
			}

			before := readSnapshot()
			if before.task == nil || before.task.Status != domain.StatusSignable {
				t.Fatalf("pre-finalize task = %+v, want SIGNABLE", before.task)
			}
			decision, finalizeErr := service.Finalize(arbiter.FinalizeRequest{
				TaskID: "T-LEASE", OperationNo: "sign", OperatorID: "arbiter", Type: arbiter.FinalSign,
			})
			after := readSnapshot()

			if tt.wantBlocked {
				if decision != nil || !domain.IsCode(finalizeErr, domain.CodeFinalizedConflict) {
					t.Fatalf("active-lease sign = (%+v, %v), want nil FINALIZED_CONFLICT", decision, finalizeErr)
				}
				var rejection *domain.Error
				if !reflect.TypeOf(finalizeErr).AssignableTo(reflect.TypeOf(rejection)) {
					t.Fatalf("finalize error type = %T, want *domain.Error", finalizeErr)
				}
				rejection = finalizeErr.(*domain.Error)
				if len(rejection.Reasons) != 1 || rejection.Reasons[0].Code != domain.CodeDeviceBusy || rejection.Reasons[0].Node != "TD-ACTIVE" {
					t.Fatalf("active-lease reasons = %+v, want DEVICE_BUSY for TD-ACTIVE", rejection.Reasons)
				}
				if !reflect.DeepEqual(after, before) {
					t.Fatalf("rejected sign mutated task, credential, lease ledger, or audit\nbefore: %+v\nafter:  %+v", before, after)
				}
				return
			}

			if finalizeErr != nil {
				t.Fatalf("finalize after lease clearance: %v", finalizeErr)
			}
			if decision == nil || decision.Credential == "" {
				t.Fatalf("final decision = %+v, want credential", decision)
			}
			if after.task == nil || after.task.Status != domain.StatusSigned || after.task.Credential != decision.Credential {
				t.Fatalf("signed task = %+v, decision = %+v", after.task, decision)
			}
			if after.decision == nil || after.decision.Credential != decision.Credential {
				t.Fatalf("persisted decision = %+v, want credential %q", after.decision, decision.Credential)
			}
			if len(after.leases) != 1 || !after.leases[0].Released {
				t.Fatalf("lease ledger = %+v, want one cleared lease", after.leases)
			}
		})
	}
}
