package task_test

import (
	"testing"

	"boltforge-highstrength-joint-qa/internal/device"
	"boltforge-highstrength-joint-qa/internal/domain"
	"boltforge-highstrength-joint-qa/internal/records"
	"boltforge-highstrength-joint-qa/internal/task"
)

func prepareTorqueRecheck(t *testing.T, taskID string, program []device.Outcome) *fixture {
	t.Helper()
	f := newFixture(t)
	f.tasks = task.NewService(f.st, f.cat, device.NewScripted(program), f.clock)
	req := lockRequest()
	req.TaskID = taskID
	req.RecheckSpec.Trials = 1
	if _, err := f.tasks.Lock(req, f.cat); err != nil {
		t.Fatalf("lock: %v", err)
	}
	for _, pair := range []struct {
		node  string
		count int
	}{{node: "N-01", count: 2}, {node: "N-02", count: 3}} {
		for bolt := 1; bolt <= pair.count; bolt++ {
			r := task.PairVerifyRequest{
				OperationNo: domain.OperationNo(taskID + "-pair-" + pair.node + "-" + itoa(bolt)),
				OperatorID:  "operator",
				NodeID:      pair.node,
				BoltNo:      bolt,
				BoltBatch:   "B-001",
				NutBatch:    "N-001",
				WasherBatch: "W-001",
				Grade:       "10.9",
				SocketSpec:  "M24",
			}
			if err := f.tasks.VerifyPair(taskID, f.rev(t, taskID), r); err != nil {
				t.Fatalf("verify pair %s/%d: %v", pair.node, bolt, err)
			}
		}
	}
	return f
}

func TestModel_TorqueRecheckDeviceFailureBoundaries(t *testing.T) {
	cases := []struct {
		name          string
		program       []device.Outcome
		wantCode      domain.Code
		wantResult    string
		wantStatus    task.Status
		wantEffective bool
	}{
		{
			name:       "start failure is pending retry",
			program:    []device.Outcome{device.OutcomeRejected},
			wantCode:   domain.CodeDeviceRejected,
			wantResult: "PENDING_RETRY",
			wantStatus: task.StatusTorqueRecheck,
		},
		{
			name:       "read failure is pending retry",
			program:    []device.Outcome{device.OutcomeOK, device.OutcomeDisconnected},
			wantCode:   domain.CodeDeviceDisconnected,
			wantResult: "PENDING_RETRY",
			wantStatus: task.StatusTorqueRecheck,
		},
		{
			name:       "stop failure discards read and is pending retry",
			program:    []device.Outcome{device.OutcomeOK, device.OutcomeOK, device.OutcomeDisconnected},
			wantCode:   domain.CodeDeviceDisconnected,
			wantResult: "PENDING_RETRY",
			wantStatus: task.StatusTorqueRecheck,
		},
		{
			name:          "complete qualified trial advances",
			wantResult:    "OK",
			wantStatus:    task.StatusInstallation,
			wantEffective: true,
		},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			taskID := "T-model-" + itoa(i+1)
			f := prepareTorqueRecheck(t, taskID, tc.program)
			op := task.RecheckRequest{
				OperationNo: domain.OperationNo(taskID + "-recheck"),
				OperatorID:  "operator",
				DeviceID:    "TD-001",
			}
			err := f.tasks.RecheckTorque(taskID, f.rev(t, taskID), op)
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("complete trial: %v", err)
				}
			} else if !domain.IsCode(err, tc.wantCode) {
				t.Fatalf("error code = %v, want %s", err, tc.wantCode)
			}

			got, err := f.tasks.Get(taskID)
			if err != nil {
				t.Fatalf("get task: %v", err)
			}
			if got.Status != tc.wantStatus {
				t.Fatalf("status = %s, want %s", got.Status, tc.wantStatus)
			}

			runs, err := records.NewService(f.st).TorqueRechecks(taskID)
			if err != nil {
				t.Fatalf("list rechecks: %v", err)
			}
			if len(runs) != 1 {
				t.Fatalf("recheck evidence count = %d, want 1", len(runs))
			}
			run := runs[0]
			if run.Result != tc.wantResult {
				t.Fatalf("result = %s, want %s", run.Result, tc.wantResult)
			}
			if run.RetrySeq != 1 {
				t.Fatalf("retry sequence = %d, want 1", run.RetrySeq)
			}
			if tc.wantEffective {
				if run.Coefficient <= 0 || run.DeviceReceipt == "" {
					t.Fatalf("successful run lacks complete reading evidence: %+v", run)
				}
			} else if run.Coefficient != 0 || run.DeviceReceipt != "" {
				t.Fatalf("failed run retained effective reading: %+v", run)
			}
		})
	}
}
