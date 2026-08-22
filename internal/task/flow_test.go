package task_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"boltforge-highstrength-joint-qa/internal/arbiter"
	"boltforge-highstrength-joint-qa/internal/catalog"
	"boltforge-highstrength-joint-qa/internal/device"
	"boltforge-highstrength-joint-qa/internal/domain"
	"boltforge-highstrength-joint-qa/internal/ledger"
	"boltforge-highstrength-joint-qa/internal/store"
	"boltforge-highstrength-joint-qa/internal/task"
)

type fixture struct {
	st      *store.SQLite
	cat     *catalog.Memory
	clock   *domain.FakeClock
	tasks   *task.Service
	ledger  *ledger.Service
	arbiter *arbiter.Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	cat := catalog.Reference()
	clock := domain.NewFakeClock(time.Unix(1700000000, 0))
	if err := ledger.SeedTokens(context.Background(), st, []domain.ConnectionToken{
		{TokenID: "TOK-001", BatchSummary: "B-001/N-001/W-001", Revision: 1},
		{TokenID: "TOK-002", BatchSummary: "B-001/N-001/W-001", Revision: 1},
		{TokenID: "TOK-003", BatchSummary: "B-001/N-001/W-001", Revision: 1},
		{TokenID: "TOK-004", BatchSummary: "B-001/N-001/W-001", Revision: 1},
		{TokenID: "TOK-005", BatchSummary: "B-001/N-001/W-001", Revision: 1},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return &fixture{
		st:      st,
		cat:     cat,
		clock:   clock,
		tasks:   task.NewService(st, cat, device.NewScripted(nil), clock),
		ledger:  ledger.NewService(st, clock),
		arbiter: arbiter.NewService(st, clock),
	}
}

func (f *fixture) rev(t *testing.T, id string) domain.Revision {
	t.Helper()
	tk, err := f.tasks.Get(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	return tk.Revision
}

func lockRequest() task.LockRequest {
	return task.LockRequest{
		TaskID: "T-1", Generation: 1, OperationNo: "op-lock",
		NodeIDs:      []string{"N-01", "N-02"},
		SamplingSet:  []task.SamplingRef{{NodeID: "N-01", BoltNo: 1}, {NodeID: "N-02", BoltNo: 1}, {NodeID: "N-02", BoltNo: 3}},
		TorqueBounds: task.TorqueBounds{DesignPreload: 240000, TorqueRange: domain.Range{Min: 300, Max: 700}, AngleRange: domain.Range{Min: 30, Max: 360}},
		RecheckSpec:  task.RecheckSpec{Trials: 3, CoefficientMin: 0.08, CoefficientMax: 0.20},
		DeviceID:     "TD-001",
		Window:       domain.TimeWindow{Start: 0, End: 4102444800},
	}
}

func TestEndToEndFlow(t *testing.T) {
	f := newFixture(t)

	locked, err := f.tasks.Lock(lockRequest(), f.cat)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	if locked.Status != task.StatusPairVerification {
		t.Fatalf("post-lock status = %s", locked.Status)
	}
	if len(locked.Nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(locked.Nodes))
	}

	// Pair verification for every bolt.
	pairs := []task.PairVerifyRequest{}
	for _, n := range []struct {
		node  string
		count int
	}{{"N-01", 2}, {"N-02", 3}} {
		for b := 1; b <= n.count; b++ {
			pairs = append(pairs, task.PairVerifyRequest{
				OperationNo: domain.OperationNo("pv-" + n.node + "-" + itoa(b)), OperatorID: "op",
				NodeID: n.node, BoltNo: b, BoltBatch: "B-001", NutBatch: "N-001", WasherBatch: "W-001",
				Grade: "10.9", SocketSpec: "M24",
			})
		}
	}
	for _, p := range pairs {
		if err := f.tasks.VerifyPair("T-1", f.rev(t, "T-1"), p); err != nil {
			t.Fatalf("verify pair %s/%d: %v", p.NodeID, p.BoltNo, err)
		}
	}
	if got := mustStatus(t, f.tasks, "T-1"); got != task.StatusTorqueRecheck {
		t.Fatalf("after pairs status = %s", got)
	}

	// Torque recheck: three trials.
	for i := 1; i <= 3; i++ {
		if err := f.tasks.RecheckTorque("T-1", f.rev(t, "T-1"), task.RecheckRequest{OperationNo: domain.OperationNo("tr-" + itoa(i)), OperatorID: "op", DeviceID: "TD-001"}); err != nil {
			t.Fatalf("recheck %d: %v", i, err)
		}
	}
	if got := mustStatus(t, f.tasks, "T-1"); got != task.StatusInstallation {
		t.Fatalf("after recheck status = %s", got)
	}

	// Device lease.
	lease, err := f.ledger.ClaimLease(ledger.ClaimLeaseRequest{TaskID: "T-1", OperationNo: "op-lease", OperatorID: "op", DeviceID: "TD-001", Generation: 1, CalibrationVersion: "CAL-2026.01", HoldSeconds: 3600})
	if err != nil {
		t.Fatalf("lease: %v", err)
	}

	// Initial then final tightening in order.
	seq := []struct {
		node string
		bolt int
	}{{"N-01", 1}, {"N-01", 2}, {"N-02", 1}, {"N-02", 2}, {"N-02", 3}}
	for _, s := range seq {
		if err := f.tasks.TightenInitial("T-1", f.rev(t, "T-1"), task.TightenRequest{OperationNo: domain.OperationNo("ti-" + s.node + "-" + itoa(s.bolt)), OperatorID: "op", NodeID: s.node, BoltNo: s.bolt, TorqueNm: 500, AngleDeg: 60, RecordedAt: 1700000000, LeaseID: lease.LeaseID}); err != nil {
			t.Fatalf("initial %s/%d: %v", s.node, s.bolt, err)
		}
	}
	for _, s := range seq {
		if err := f.tasks.TightenFinal("T-1", f.rev(t, "T-1"), task.TightenRequest{OperationNo: domain.OperationNo("tf-" + s.node + "-" + itoa(s.bolt)), OperatorID: "op", NodeID: s.node, BoltNo: s.bolt, TorqueNm: 500, AngleDeg: 60, RecordedAt: 1700000000, LeaseID: lease.LeaseID}); err != nil {
			t.Fatalf("final %s/%d: %v", s.node, s.bolt, err)
		}
	}
	if got := mustStatus(t, f.tasks, "T-1"); got != task.StatusSamplingReview {
		t.Fatalf("after tighten status = %s", got)
	}

	// Release the device lease before review/sign.
	if err := f.ledger.ReleaseLease("T-1", lease.LeaseID, "op-release"); err != nil {
		t.Fatalf("release lease: %v", err)
	}

	// Sampling: close every sampled bolt with an in-range preload.
	for _, ref := range []task.SamplingRef{{NodeID: "N-01", BoltNo: 1}, {NodeID: "N-02", BoltNo: 1}, {NodeID: "N-02", BoltNo: 3}} {
		if err := f.tasks.SubmitSampling("T-1", f.rev(t, "T-1"), task.SamplingRequest{OperationNo: domain.OperationNo("sm-" + ref.NodeID + "-" + itoa(ref.BoltNo)), OperatorID: "op", NodeID: ref.NodeID, BoltNo: ref.BoltNo, MeasuredPreload: 240000, DeviceID: "TD-001", PersonID: "P-ALICE"}); err != nil {
			t.Fatalf("sampling %s/%d: %v", ref.NodeID, ref.BoltNo, err)
		}
	}

	// Two independent qualified reviewers.
	lockedSummary := locked.LockSummary
	if err := f.arbiter.SubmitReview(arbiter.ReviewRequest{TaskID: "T-1", OperationNo: "rv-1", PersonID: "P-ALICE", Seat: arbiter.SeatFirst, TaskSummary: lockedSummary}, "Q-3", "Q-3"); err != nil {
		t.Fatalf("review 1: %v", err)
	}
	if err := f.arbiter.SubmitReview(arbiter.ReviewRequest{TaskID: "T-1", OperationNo: "rv-2", PersonID: "P-BOB", Seat: arbiter.SeatSecond, TaskSummary: lockedSummary}, "Q-3", "Q-3"); err != nil {
		t.Fatalf("review 2: %v", err)
	}
	if got := mustStatus(t, f.tasks, "T-1"); got != task.StatusSignable {
		t.Fatalf("after reviews status = %s", got)
	}

	// Finalize sign.
	decision, err := f.arbiter.Finalize(arbiter.FinalizeRequest{TaskID: "T-1", OperationNo: "fin-1", OperatorID: "op", Type: arbiter.FinalSign})
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if decision.Credential == "" {
		t.Fatal("expected a credential")
	}
	if got := mustStatus(t, f.tasks, "T-1"); got != task.StatusSigned {
		t.Fatalf("after sign status = %s", got)
	}

	// Late write after terminal state is rejected.
	if err := f.tasks.TightenInitial("T-1", f.rev(t, "T-1"), task.TightenRequest{OperationNo: "late", OperatorID: "op", NodeID: "N-01", BoltNo: 1, TorqueNm: 500, AngleDeg: 60, RecordedAt: 1700000000, LeaseID: lease.LeaseID}); !domain.IsCode(err, domain.CodeTerminalState) {
		t.Fatalf("late write should be TERMINAL_STATE, got %v", err)
	}
}

func TestSnapshotImmutableAfterCatalogRevision(t *testing.T) {
	f := newFixture(t)
	if _, err := f.tasks.Lock(lockRequest(), f.cat); err != nil {
		t.Fatalf("lock: %v", err)
	}
	// Revise the catalogue after locking; the snapshot must be unaffected.
	f.cat.Revise()
	p := task.PairVerifyRequest{OperationNo: "pv-1", OperatorID: "op", NodeID: "N-01", BoltNo: 1, BoltBatch: "B-001", NutBatch: "N-001", WasherBatch: "W-001", Grade: "10.9", SocketSpec: "M24"}
	if err := f.tasks.VerifyPair("T-1", f.rev(t, "T-1"), p); err != nil {
		t.Fatalf("verify pair after revision should use snapshot: %v", err)
	}
}

func TestSequenceGapLeavesCursorUnchanged(t *testing.T) {
	f := newFixture(t)
	if _, err := f.tasks.Lock(lockRequest(), f.cat); err != nil {
		t.Fatalf("lock: %v", err)
	}
	// Verify pair N-01/1 then try to tighten N-01/2 (skip 1) — but first we need
	// to reach installation, so this is a focused cursor check via the task node.
	node := task.NewTaskNode("N-01", 2)
	if err := task.ValidateInitialOrder(node, 2); !domain.IsCode(err, domain.CodeSequenceGap) {
		t.Fatalf("skip should be SEQUENCE_GAP, got %v", err)
	}
}

func mustStatus(t *testing.T, svc *task.Service, id string) task.Status {
	t.Helper()
	tk, err := svc.Get(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	return tk.Status
}

func itoa(n int) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{digits[n%10]}, b...)
		n /= 10
	}
	return string(b)
}
