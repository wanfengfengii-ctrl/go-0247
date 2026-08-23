package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"boltforge-highstrength-joint-qa/internal/api"
	"boltforge-highstrength-joint-qa/internal/catalog"
	"boltforge-highstrength-joint-qa/internal/device"
	"boltforge-highstrength-joint-qa/internal/domain"
	"boltforge-highstrength-joint-qa/internal/ledger"
	"boltforge-highstrength-joint-qa/internal/store"
	"boltforge-highstrength-joint-qa/internal/task"
)

func TestModel_ExpiredLeaseIsReclaimedAndHiddenFromTaskQueries(t *testing.T) {
	cases := []struct {
		name    string
		advance time.Duration
	}{
		{name: "at_expiry", advance: time.Second},
		{name: "after_expiry", advance: 2 * time.Second},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, err := store.Open(filepath.Join(t.TempDir(), "lease-expiry.db"))
			if err != nil {
				t.Fatalf("open store: %v", err)
			}
			defer st.Close()

			clock := domain.NewFakeClock(time.Unix(1700000000, 0))
			cat := catalog.Reference()
			tasks := task.NewService(st, cat, device.NewScripted(nil), clock)
			led := ledger.NewService(st, clock)
			if _, err := tasks.Lock(task.LockRequest{
				TaskID: "T-OLD", Generation: 1, OperationNo: "lock-old",
				NodeIDs: []string{"N-01"}, DeviceID: "TD-001",
				TorqueBounds: task.TorqueBounds{
					DesignPreload: 240000,
					TorqueRange:   domain.Range{Min: 300, Max: 700},
					AngleRange:    domain.Range{Min: 30, Max: 360},
				},
				RecheckSpec: task.RecheckSpec{Trials: 1, CoefficientMin: 0.08, CoefficientMax: 0.20},
				Window:      domain.TimeWindow{Start: 0, End: 4102444800},
			}, cat); err != nil {
				t.Fatalf("lock old task: %v", err)
			}

			oldLease, err := led.ClaimLease(ledger.ClaimLeaseRequest{
				TaskID: "T-OLD", OperationNo: "claim-old", OperatorID: "old-operator",
				DeviceID: "TD-001", Generation: 1, CalibrationVersion: "CAL-2026.01", HoldSeconds: 1,
			})
			if err != nil {
				t.Fatalf("claim old lease: %v", err)
			}

			clock.Advance(tc.advance)
			h := api.NewHandler(api.Deps{
				Catalog: cat, Tasks: tasks, Ledger: led, Store: st, Clock: clock,
			})

			claimReq := httptest.NewRequest(http.MethodPost, "/v1/tasks/T-NEW/leases/claim", strings.NewReader(`{"device_id":"TD-001","generation":2,"hold_seconds":60,"operation_no":"claim-new","operator_id":"new-operator"}`))
			claimRR := httptest.NewRecorder()
			h.ServeHTTP(claimRR, claimReq)
			if claimRR.Code != http.StatusCreated {
				t.Fatalf("new task claim status = %d, want 201; body=%s", claimRR.Code, claimRR.Body.String())
			}

			getReq := httptest.NewRequest(http.MethodGet, "/v1/tasks/T-OLD", nil)
			getRR := httptest.NewRecorder()
			h.ServeHTTP(getRR, getReq)
			if getRR.Code != http.StatusOK {
				t.Fatalf("old task query status = %d, want 200; body=%s", getRR.Code, getRR.Body.String())
			}
			var response struct {
				Data api.TaskView `json:"data"`
			}
			if err := json.Unmarshal(getRR.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode old task query: %v", err)
			}
			if len(response.Data.ActiveLeases) != 0 {
				t.Fatalf("expired lease %s remained active: %+v", oldLease.LeaseID, response.Data.ActiveLeases)
			}

			seenClaim := false
			for _, kind := range response.Data.AuditKinds {
				if kind == "CLAIM_LEASE" {
					seenClaim = true
					break
				}
			}
			if !seenClaim {
				t.Fatalf("old lease claim was not retained in audit history: %v", response.Data.AuditKinds)
			}

			var stored *domain.DeviceLease
			if err := st.WithTx(context.Background(), func(tx store.Tx) error {
				var err error
				stored, err = tx.GetLease(context.Background(), oldLease.LeaseID)
				return err
			}); err != nil {
				t.Fatalf("read expired lease history: %v", err)
			}
			if stored == nil {
				t.Fatal("expired lease disappeared from auditable lease history")
			}
		})
	}
}
