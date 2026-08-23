package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"boltforge-highstrength-joint-qa/internal/api"
	"boltforge-highstrength-joint-qa/internal/arbiter"
)

type finalizeIdentityArbiter struct {
	calls     []arbiter.FinalizeRequest
	mutations map[string]int
}

func (a *finalizeIdentityArbiter) SubmitReview(arbiter.ReviewRequest, string, string) error {
	return nil
}

func (a *finalizeIdentityArbiter) Finalize(req arbiter.FinalizeRequest) (*arbiter.FinalDecision, error) {
	a.calls = append(a.calls, req)
	a.mutations[req.TaskID]++
	return &arbiter.FinalDecision{
		TaskID:           req.TaskID,
		Type:             req.Type,
		WinningOperation: req.OperationNo,
		Credential:       "credential-for-" + req.TaskID,
	}, nil
}

func TestModel_FinalizeUsesURLTaskIdentity(t *testing.T) {
	tests := []struct {
		name       string
		endpoint   string
		finalType  arbiter.FinalType
		body       string
		wantStatus int
		wantCalls  int
		wantCode   string
	}{
		{name: "sign fills omitted task id", endpoint: "sign", finalType: arbiter.FinalSign, body: `{"operation_no":"op-sign","operator_id":"operator"}`, wantStatus: http.StatusOK, wantCalls: 1},
		{name: "sign rejects mismatched task id", endpoint: "sign", finalType: arbiter.FinalSign, body: `{"task_id":"task-body","operation_no":"op-sign","operator_id":"operator"}`, wantStatus: http.StatusBadRequest, wantCode: "TASK_ID_MISMATCH"},
		{name: "quarantine fills omitted task id", endpoint: "quarantine", finalType: arbiter.FinalQuarantine, body: `{"operation_no":"op-quarantine","operator_id":"operator"}`, wantStatus: http.StatusOK, wantCalls: 1},
		{name: "quarantine rejects mismatched task id", endpoint: "quarantine", finalType: arbiter.FinalQuarantine, body: `{"task_id":"task-body","operation_no":"op-quarantine","operator_id":"operator"}`, wantStatus: http.StatusBadRequest, wantCode: "TASK_ID_MISMATCH"},
		{name: "cancel fills omitted task id", endpoint: "cancel", finalType: arbiter.FinalCancel, body: `{"operation_no":"op-cancel","operator_id":"operator"}`, wantStatus: http.StatusOK, wantCalls: 1},
		{name: "cancel rejects mismatched task id", endpoint: "cancel", finalType: arbiter.FinalCancel, body: `{"task_id":"task-body","operation_no":"op-cancel","operator_id":"operator"}`, wantStatus: http.StatusBadRequest, wantCode: "TASK_ID_MISMATCH"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &finalizeIdentityArbiter{mutations: map[string]int{
				"task-url":  0,
				"task-body": 0,
			}}
			h := api.NewHandler(api.Deps{Arbiter: fake})
			req := httptest.NewRequest(http.MethodPost, "/v1/tasks/task-url/finalize/"+tt.endpoint, strings.NewReader(tt.body))
			rr := httptest.NewRecorder()

			h.ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", rr.Code, tt.wantStatus, rr.Body.String())
			}
			if len(fake.calls) != tt.wantCalls {
				t.Fatalf("arbiter calls = %d, want %d", len(fake.calls), tt.wantCalls)
			}

			if tt.wantCalls == 1 {
				got := fake.calls[0]
				if got.TaskID != "task-url" {
					t.Errorf("arbiter task id = %q, want URL task %q", got.TaskID, "task-url")
				}
				if got.Type != tt.finalType {
					t.Errorf("final type = %q, want %q", got.Type, tt.finalType)
				}
				if fake.mutations["task-url"] != 1 || fake.mutations["task-body"] != 0 {
					t.Errorf("mutations = %#v, want only URL task mutated", fake.mutations)
				}
				return
			}

			var env api.Envelope
			if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if env.Error == nil || env.Error.Code != tt.wantCode {
				t.Errorf("error = %#v, want code %q", env.Error, tt.wantCode)
			}
			if fake.mutations["task-url"] != 0 || fake.mutations["task-body"] != 0 {
				t.Errorf("rejected request mutated task state: %#v", fake.mutations)
			}
		})
	}
}
