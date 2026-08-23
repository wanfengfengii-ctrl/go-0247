package task_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"boltforge-highstrength-joint-qa/internal/catalog"
	"boltforge-highstrength-joint-qa/internal/device"
	"boltforge-highstrength-joint-qa/internal/domain"
	"boltforge-highstrength-joint-qa/internal/ledger"
	"boltforge-highstrength-joint-qa/internal/records"
	"boltforge-highstrength-joint-qa/internal/store"
	"boltforge-highstrength-joint-qa/internal/task"
)

type readFailureDevice struct {
	readErr      error
	staleSession error
	active       bool
	starts       int
	reads        int
	stops        int
	receipts     int
}

func (d *readFailureDevice) Start(context.Context, device.StartRequest) error {
	d.starts++
	if d.active {
		return d.staleSession
	}
	d.active = true
	return nil
}

func (d *readFailureDevice) Read(context.Context, device.ReadRequest) (device.Reading, error) {
	d.reads++
	if d.readErr != nil {
		err := d.readErr
		d.readErr = nil
		return device.Reading{}, err
	}
	d.receipts++
	return device.Reading{Coefficient: 0.12, Receipt: fmt.Sprintf("retry-receipt-%d", d.receipts)}, nil
}

func (d *readFailureDevice) Stop(context.Context, device.StopRequest) error {
	d.stops++
	d.active = false
	return nil
}

func prepareReadFailureTask(t *testing.T, dev device.Adapter) (*store.SQLite, *task.Service) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "task.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cat := catalog.Reference()
	if err := ledger.SeedTokens(context.Background(), st, []domain.ConnectionToken{
		{TokenID: "TOK-001", BatchSummary: "B-001/N-001/W-001", Revision: 1},
		{TokenID: "TOK-002", BatchSummary: "B-001/N-001/W-001", Revision: 1},
		{TokenID: "TOK-003", BatchSummary: "B-001/N-001/W-001", Revision: 1},
		{TokenID: "TOK-004", BatchSummary: "B-001/N-001/W-001", Revision: 1},
		{TokenID: "TOK-005", BatchSummary: "B-001/N-001/W-001", Revision: 1},
	}); err != nil {
		t.Fatalf("seed tokens: %v", err)
	}
	svc := task.NewService(st, cat, dev, domain.NewFakeClock(time.Unix(1700000000, 0)))
	req := lockRequest()
	req.RecheckSpec = task.RecheckSpec{Trials: 1, CoefficientMin: 0.08, CoefficientMax: 0.20}
	if _, err := svc.Lock(req, cat); err != nil {
		t.Fatalf("lock: %v", err)
	}
	for _, node := range []struct {
		id    string
		count int
	}{{"N-01", 2}, {"N-02", 3}} {
		for boltNo := 1; boltNo <= node.count; boltNo++ {
			if err := svc.VerifyPair("T-1", currentTaskRevision(t, svc), task.PairVerifyRequest{
				OperationNo: domain.OperationNo(fmt.Sprintf("pair-%s-%d", node.id, boltNo)), OperatorID: "op",
				NodeID: node.id, BoltNo: boltNo, BoltBatch: "B-001", NutBatch: "N-001", WasherBatch: "W-001",
				Grade: "10.9", SocketSpec: "M24",
			}); err != nil {
				t.Fatalf("verify pair %s/%d: %v", node.id, boltNo, err)
			}
		}
	}
	return st, svc
}

func currentTaskRevision(t *testing.T, svc *task.Service) domain.Revision {
	t.Helper()
	tk, err := svc.Get("T-1")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	return tk.Revision
}

func TestModel_DeviceReadFailureReleasesSessionAndPersistsRetryEvidence(t *testing.T) {
	cases := []struct {
		name string
		code domain.Code
		text string
	}{
		{name: "disconnected", code: domain.CodeDeviceDisconnected, text: "read disconnected"},
		{name: "rejected", code: domain.CodeDeviceRejected, text: "read rejected"},
		{name: "calibration_expired", code: domain.CodeCalibrationExpired, text: "read calibration expired"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			readErr := domain.NewError(tc.code, tc.text)
			dev := &readFailureDevice{
				readErr:      readErr,
				staleSession: domain.NewError(domain.CodeDeviceDisconnected, "stale device session"),
			}
			st, svc := prepareReadFailureTask(t, dev)
			recordsView := records.NewService(st)

			firstErr := svc.RecheckTorque("T-1", currentTaskRevision(t, svc), task.RecheckRequest{
				OperationNo: "recheck-failed", OperatorID: "op", DeviceID: "TD-001",
			})
			if !domain.IsCode(firstErr, tc.code) || firstErr.Error() != tc.text {
				t.Fatalf("read error = %v, want %s with original message", firstErr, tc.code)
			}
			if dev.starts != 1 || dev.reads != 1 || dev.stops != 1 || dev.active {
				t.Fatalf("failed trial lifecycle = starts:%d reads:%d stops:%d active:%v, want 1/1/1/false", dev.starts, dev.reads, dev.stops, dev.active)
			}

			runs, err := recordsView.TorqueRechecks("T-1")
			if err != nil {
				t.Fatalf("list failed evidence: %v", err)
			}
			if len(runs) != 1 || runs[0].RetrySeq != 1 || runs[0].Result != "PENDING_RETRY" || runs[0].DeviceReceipt != "" {
				t.Fatalf("failed evidence = %#v, want one pending retry without receipt", runs)
			}

			if err := svc.RecheckTorque("T-1", currentTaskRevision(t, svc), task.RecheckRequest{
				OperationNo: "recheck-retry", OperatorID: "op", DeviceID: "TD-001",
			}); err != nil {
				t.Fatalf("retry should succeed after failed read cleanup: %v", err)
			}
			if dev.starts != 2 || dev.reads != 2 || dev.stops != 2 || dev.active {
				t.Fatalf("retry lifecycle = starts:%d reads:%d stops:%d active:%v, want 2/2/2/false", dev.starts, dev.reads, dev.stops, dev.active)
			}
			runs, err = recordsView.TorqueRechecks("T-1")
			if err != nil {
				t.Fatalf("list retry evidence: %v", err)
			}
			if len(runs) != 2 || runs[1].RetrySeq != 2 || runs[1].Result != "OK" || runs[1].DeviceReceipt == "" {
				t.Fatalf("retry evidence = %#v, want second successful run", runs)
			}
			if tk, err := svc.Get("T-1"); err != nil || tk.Status != task.StatusInstallation {
				t.Fatalf("status after successful retry = %v (err=%v), want installation", tk.Status, err)
			}
		})
	}
}
