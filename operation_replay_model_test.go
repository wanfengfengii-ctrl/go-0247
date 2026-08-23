package task_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"boltforge-highstrength-joint-qa/internal/catalog"
	"boltforge-highstrength-joint-qa/internal/device"
	"boltforge-highstrength-joint-qa/internal/domain"
	"boltforge-highstrength-joint-qa/internal/store"
	"boltforge-highstrength-joint-qa/internal/task"
)

type replayCountingAdapter struct {
	scripted *device.Scripted
	starts   int
	reads    int
	stops    int
}

func (a *replayCountingAdapter) Start(ctx context.Context, req device.StartRequest) error {
	a.starts++
	return a.scripted.Start(ctx, req)
}

func (a *replayCountingAdapter) Read(ctx context.Context, req device.ReadRequest) (device.Reading, error) {
	a.reads++
	return a.scripted.Read(ctx, req)
}

func (a *replayCountingAdapter) Stop(ctx context.Context, req device.StopRequest) error {
	a.stops++
	return a.scripted.Stop(ctx, req)
}

func TestModel_OperationReplayPreservesFailure(t *testing.T) {
	cases := []struct {
		name    string
		outcome device.Outcome
		code    domain.Code
	}{
		{name: "disconnected", outcome: device.OutcomeDisconnected, code: domain.CodeDeviceDisconnected},
		{name: "rejected", outcome: device.OutcomeRejected, code: domain.CodeDeviceRejected},
		{name: "calibration_expired", outcome: device.OutcomeCalibrationExpired, code: domain.CodeCalibrationExpired},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, err := store.Open(filepath.Join(t.TempDir(), "replay.db"))
			if err != nil {
				t.Fatalf("open store: %v", err)
			}
			defer st.Close()

			if err := st.WithTx(context.Background(), func(tx store.Tx) error {
				return tx.SaveTask(context.Background(), &domain.InspectionTask{
					TaskID:           "T-REPLAY",
					Generation:       1,
					Status:           domain.StatusTorqueRecheck,
					Revision:         1,
					DeviceCalVersion: "CAL-2026.01",
					RecheckSpec:      domain.RecheckSpec{Trials: 1, CoefficientMin: 0.08, CoefficientMax: 0.20},
				}, 0)
			}); err != nil {
				t.Fatalf("seed task: %v", err)
			}

			adapter := &replayCountingAdapter{scripted: device.NewScripted([]device.Outcome{tc.outcome})}
			svc := task.NewService(st, catalog.Reference(), adapter, domain.NewFakeClock(time.Unix(1700000000, 0)))
			req := task.RecheckRequest{
				OperationNo: "op-replay-failure",
				OperatorID:  "operator-1",
				Revision:    1,
				DeviceID:    "TD-001",
			}

			firstErr := svc.RecheckTorque("T-REPLAY", 1, req)
			if !domain.IsCode(firstErr, tc.code) {
				t.Fatalf("first attempt: want %s, got %v", tc.code, firstErr)
			}

			var runs []domain.TorqueCoefficientRun
			var audit []store.AuditEvent
			var entry store.IdempotencyEntry
			var found bool
			if err := st.WithTx(context.Background(), func(tx store.Tx) error {
				var err error
				runs, err = tx.ListTorqueRechecks(context.Background(), "T-REPLAY")
				if err != nil {
					return err
				}
				audit, err = tx.ListAudit(context.Background(), "T-REPLAY")
				if err != nil {
					return err
				}
				entry, found, err = tx.GetIdempotency(context.Background(), req.OperationNo, "T-REPLAY")
				return err
			}); err != nil {
				t.Fatalf("inspect committed failure: %v", err)
			}
			if len(runs) != 1 || runs[0].OperationNo != req.OperationNo || runs[0].RetrySeq != 1 || runs[0].Result != "PENDING_RETRY" {
				t.Fatalf("failure evidence = %+v, want one pending retry for the operation", runs)
			}
			if len(audit) != 1 || audit[0].Operation != req.OperationNo || audit[0].Kind != "TORQUE_RECHECK" {
				t.Fatalf("failure audit = %+v, want one committed operation entry", audit)
			}
			if !found || entry.Digest == "" || entry.Result == "" {
				t.Fatalf("idempotency entry = %+v, want digest and original result", entry)
			}
			if replayErr := domain.DecodeIdempotencyResult(entry.Result); !domain.IsCode(replayErr, tc.code) {
				t.Fatalf("persisted result: want %s, got %v", tc.code, replayErr)
			}

			secondErr := svc.RecheckTorque("T-REPLAY", 1, req)
			if !domain.IsCode(secondErr, tc.code) {
				t.Fatalf("replay: want original %s, got %v", tc.code, secondErr)
			}
			if entry.Result != domain.EncodeIdempotencyResult(secondErr) {
				t.Fatalf("replay result changed: stored %q, replay %q", entry.Result, domain.EncodeIdempotencyResult(secondErr))
			}

			if err := st.WithTx(context.Background(), func(tx store.Tx) error {
				var err error
				runs, err = tx.ListTorqueRechecks(context.Background(), "T-REPLAY")
				if err != nil {
					return err
				}
				audit, err = tx.ListAudit(context.Background(), "T-REPLAY")
				return err
			}); err != nil {
				t.Fatalf("inspect replay evidence: %v", err)
			}
			if len(runs) != 1 {
				t.Fatalf("replay wrote duplicate evidence: %+v", runs)
			}
			if len(audit) != 1 {
				t.Fatalf("replay wrote duplicate audit evidence: %+v", audit)
			}
			if adapter.starts != 1 || adapter.reads != 0 || adapter.stops != 0 {
				t.Fatalf("replay opened another device session: starts=%d reads=%d stops=%d", adapter.starts, adapter.reads, adapter.stops)
			}
		})
	}
}
