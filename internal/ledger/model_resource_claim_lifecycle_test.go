package ledger_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"boltforge-highstrength-joint-qa/internal/domain"
	"boltforge-highstrength-joint-qa/internal/ledger"
	"boltforge-highstrength-joint-qa/internal/store"
)

func TestModel_ResourceClaimRejectsInvalidTaskBeforeOccupancy(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1700000000, 0)

	type claimKind string
	const (
		claimLease claimKind = "lease"
		claimToken claimKind = "token"
	)

	tests := []struct {
		name          string
		kind          claimKind
		invalidTaskID string
		invalidStatus domain.Status
		invalidGen    domain.Generation
		requestGen    domain.Generation
		wantCode      domain.Code
		seedInvalid   bool
		seedToken     bool
	}{
		{
			name:          "lease missing task leaves device available",
			kind:          claimLease,
			invalidTaskID: "T-MISSING",
			requestGen:    7,
			wantCode:      domain.CodeNodeNotFound,
		},
		{
			name:          "lease generation mismatch leaves device available",
			kind:          claimLease,
			invalidTaskID: "T-WRONG-GEN",
			invalidStatus: domain.StatusInstallation,
			invalidGen:    4,
			requestGen:    5,
			wantCode:      domain.CodeGenerationMismatch,
			seedInvalid:   true,
		},
		{
			name:          "lease terminal task leaves device available",
			kind:          claimLease,
			invalidTaskID: "T-SIGNED",
			invalidStatus: domain.StatusSigned,
			invalidGen:    7,
			requestGen:    7,
			wantCode:      domain.CodeTerminalState,
			seedInvalid:   true,
		},
		{
			name:          "token missing task leaves token available",
			kind:          claimToken,
			invalidTaskID: "T-MISSING-TOKEN",
			wantCode:      domain.CodeNodeNotFound,
			seedToken:     true,
		},
		{
			name:          "token terminal task leaves token available",
			kind:          claimToken,
			invalidTaskID: "T-CANCELLED",
			invalidStatus: domain.StatusCancelled,
			invalidGen:    9,
			wantCode:      domain.CodeTerminalState,
			seedInvalid:   true,
			seedToken:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatalf("open store: %v", err)
			}
			t.Cleanup(func() { db.Close() })

			svc := ledger.NewService(db, domain.NewFakeClock(now))
			const validTaskID = "T-OPEN"
			const deviceID = "TD-SHARED"
			const tokenID = "TOK-SHARED"

			if err := db.WithTx(ctx, func(tx store.Tx) error {
				if tt.seedInvalid {
					if err := tx.SaveTask(ctx, &domain.InspectionTask{
						TaskID: tt.invalidTaskID, Generation: tt.invalidGen, Status: tt.invalidStatus, Revision: 1, LockSummary: "invalid",
					}, 0); err != nil {
						return err
					}
				}
				if err := tx.SaveTask(ctx, &domain.InspectionTask{
					TaskID: validTaskID, Generation: 1, Status: domain.StatusInstallation, Revision: 1, LockSummary: "valid",
				}, 0); err != nil {
					return err
				}
				if tt.seedToken {
					return tx.InsertToken(ctx, domain.ConnectionToken{TokenID: tokenID, BatchSummary: "bolt/nut/washer", Revision: 1})
				}
				return nil
			}); err != nil {
				t.Fatalf("seed store: %v", err)
			}

			switch tt.kind {
			case claimLease:
				_, err = svc.ClaimLease(ledger.ClaimLeaseRequest{
					TaskID: tt.invalidTaskID, OperationNo: "op-invalid-lease", OperatorID: "operator-1",
					DeviceID: deviceID, Generation: tt.requestGen, CalibrationVersion: "CAL-1", HoldSeconds: 3600,
				})
				if !domain.IsCode(err, tt.wantCode) {
					t.Fatalf("invalid lease claim error = %v, want %s", err, tt.wantCode)
				}
				if err := db.WithTx(ctx, func(tx store.Tx) error {
					leases, err := tx.ListLeases(ctx, tt.invalidTaskID)
					if err != nil {
						return err
					}
					if len(leases) != 0 {
						t.Fatalf("invalid task has leases after rejected claim: %+v", leases)
					}
					return nil
				}); err != nil {
					t.Fatalf("inspect invalid leases: %v", err)
				}
				if got, err := svc.ClaimLease(ledger.ClaimLeaseRequest{
					TaskID: validTaskID, OperationNo: "op-valid-lease", OperatorID: "operator-2",
					DeviceID: deviceID, Generation: 1, CalibrationVersion: "CAL-1", HoldSeconds: 3600,
				}); err != nil {
					t.Fatalf("valid lease claim after rejected claim: %v", err)
				} else if got.TaskID != validTaskID || got.DeviceID != deviceID {
					t.Fatalf("valid lease bound to wrong target: %+v", got)
				}
			case claimToken:
				_, err = svc.ClaimToken(ledger.ClaimTokenRequest{
					TaskID: tt.invalidTaskID, OperationNo: "op-invalid-token", OperatorID: "operator-1",
					TokenID: tokenID, NodeID: "N-01", BoltNo: 1,
				})
				if !domain.IsCode(err, tt.wantCode) {
					t.Fatalf("invalid token claim error = %v, want %s", err, tt.wantCode)
				}
				if got, err := svc.ClaimToken(ledger.ClaimTokenRequest{
					TaskID: validTaskID, OperationNo: "op-valid-token", OperatorID: "operator-2",
					TokenID: tokenID, NodeID: "N-01", BoltNo: 1,
				}); err != nil {
					t.Fatalf("valid token claim after rejected claim: %v", err)
				} else if got.TaskID != validTaskID || got.TokenID != tokenID {
					t.Fatalf("valid token bound to wrong target: %+v", got)
				}
			default:
				t.Fatalf("unknown claim kind %q", tt.kind)
			}
		})
	}
}
