package task_test

import (
	"context"
	"testing"

	"boltforge-highstrength-joint-qa/internal/domain"
	"boltforge-highstrength-joint-qa/internal/store"
	"boltforge-highstrength-joint-qa/internal/task"
)

func TestModel_SamplingFailureTerminalDecision(t *testing.T) {
	cases := []struct {
		name       string
		measured   int
		wantResult domain.SampleResult
	}{
		{name: "below-allowable-preload", measured: 84, wantResult: domain.SampleLow},
		{name: "above-allowable-preload", measured: 116, wantResult: domain.SampleHigh},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			taskID := "T-sampling-" + tc.name
			operation := domain.OperationNo("sample-" + tc.name)
			seeded := &domain.InspectionTask{
				TaskID:     taskID,
				Generation: 1,
				Status:     domain.StatusSamplingReview,
				Revision:   1,
				SamplingSet: []domain.SamplingRef{{
					NodeID: "N-01",
					BoltNo: 1,
				}},
				TorqueBounds: domain.TorqueBounds{DesignPreload: 100},
			}
			if err := f.st.WithTx(context.Background(), func(tx store.Tx) error {
				return tx.SaveTask(context.Background(), seeded, 0)
			}); err != nil {
				t.Fatalf("seed task: %v", err)
			}

			err := f.tasks.SubmitSampling(taskID, seeded.Revision, task.SamplingRequest{
				OperationNo:     operation,
				OperatorID:      "operator-1",
				NodeID:          "N-01",
				BoltNo:          1,
				MeasuredPreload: tc.measured,
				DeviceID:        "TD-001",
				PersonID:        "P-ALICE",
			})
			if !domain.IsCode(err, domain.CodeSampleOutOfRange) {
				t.Fatalf("sampling failure error = %v, want SAMPLE_OUT_OF_RANGE", err)
			}

			current, err := f.tasks.Get(taskID)
			if err != nil {
				t.Fatalf("load task: %v", err)
			}
			if current.Status != domain.StatusQuarantined {
				t.Fatalf("status = %s, want QUARANTINED", current.Status)
			}
			if current.Credential == "" {
				t.Fatal("quarantined task has no terminal credential")
			}

			var decision *domain.FinalDecision
			var sampling []domain.SamplingResult
			if err := f.st.WithTx(context.Background(), func(tx store.Tx) error {
				var err error
				decision, err = tx.GetDecision(context.Background(), taskID)
				if err != nil {
					return err
				}
				sampling, err = tx.ListSamplingResults(context.Background(), taskID)
				return err
			}); err != nil {
				t.Fatalf("read terminal evidence: %v", err)
			}
			if decision == nil {
				t.Fatal("sampling failure left no final decision")
			}
			if decision.Type != domain.FinalQuarantine {
				t.Fatalf("decision type = %s, want QUARANTINE", decision.Type)
			}
			if decision.WinningOperation != operation {
				t.Fatalf("winning operation = %s, want %s", decision.WinningOperation, operation)
			}
			if decision.Credential == "" || decision.Credential != current.Credential {
				t.Fatalf("decision credential = %q, task credential = %q", decision.Credential, current.Credential)
			}
			if decision.ReasonSummary == "" {
				t.Fatal("quarantine decision has no reason summary")
			}
			if len(sampling) != 1 || sampling[0].Result != tc.wantResult || sampling[0].OperationNo != operation {
				t.Fatalf("sampling records = %+v, want one %s record for %s", sampling, tc.wantResult, operation)
			}
		})
	}
}
