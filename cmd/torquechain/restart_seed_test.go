package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"boltforge-highstrength-joint-qa/internal/api"
	"boltforge-highstrength-joint-qa/internal/arbiter"
	"boltforge-highstrength-joint-qa/internal/catalog"
	"boltforge-highstrength-joint-qa/internal/device"
	"boltforge-highstrength-joint-qa/internal/domain"
	"boltforge-highstrength-joint-qa/internal/ledger"
	"boltforge-highstrength-joint-qa/internal/records"
	"boltforge-highstrength-joint-qa/internal/store"
	"boltforge-highstrength-joint-qa/internal/task"
)

func TestModel_RestartSeedTokensAreIdempotentAndServiceStaysAvailable(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(*testing.T, *store.SQLite)
		assert  func(*testing.T, *store.SQLite)
	}{
		{
			name: "already seeded database",
		},
		{
			name: "claimed seed token is preserved",
			prepare: func(t *testing.T, st *store.SQLite) {
				t.Helper()
				svc := ledger.NewService(st, domain.NewFakeClock(time.Unix(1700000000, 0)))
				if _, err := svc.ClaimToken(ledger.ClaimTokenRequest{
					TaskID:      "T-claimed",
					OperationNo: "op-claim",
					OperatorID:  "qa-1",
					TokenID:     "TOK-001",
					NodeID:      "N-01",
					BoltNo:      1,
				}); err != nil {
					t.Fatalf("claim seed token before restart: %v", err)
				}
			},
			assert: func(t *testing.T, st *store.SQLite) {
				t.Helper()
				svc := ledger.NewService(st, domain.NewFakeClock(time.Unix(1700000000, 0)))
				tok, err := svc.ClaimToken(ledger.ClaimTokenRequest{
					TaskID:      "T-other",
					OperationNo: "op-reclaim",
					OperatorID:  "qa-2",
					TokenID:     "TOK-001",
					NodeID:      "N-01",
					BoltNo:      1,
				})
				if tok != nil || !domain.IsCode(err, domain.CodeTokenBusy) {
					t.Fatalf("claimed token after restart seed = %+v, %v; want TOKEN_BUSY", tok, err)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "torquechain.db")
			st, err := store.Open(dbPath)
			if err != nil {
				t.Fatalf("open initial store: %v", err)
			}
			if err := seedTokens(st); err != nil {
				t.Fatalf("initial seed tokens: %v", err)
			}
			if tc.prepare != nil {
				tc.prepare(t, st)
			}
			if err := st.Close(); err != nil {
				t.Fatalf("close initial store: %v", err)
			}

			restarted, err := store.Open(dbPath)
			if err != nil {
				t.Fatalf("reopen existing store: %v", err)
			}
			defer restarted.Close()
			if err := seedTokens(restarted); err != nil {
				t.Fatalf("restart seed tokens: %v", err)
			}
			if err := seedTokens(restarted); err != nil {
				t.Fatalf("repeated restart seed tokens: %v", err)
			}
			if tc.assert != nil {
				tc.assert(t, restarted)
			}

			cat := catalog.Reference()
			clock := domain.NewFakeClock(time.Unix(1700000000, 0))
			h := api.NewHandler(api.Deps{
				Catalog: cat,
				Tasks:   task.NewService(restarted, cat, device.NewScripted(nil), clock),
				Ledger:  ledger.NewService(restarted, clock),
				Records: records.NewService(restarted),
				Arbiter: arbiter.NewService(restarted, clock),
				Store:   restarted,
				Clock:   clock,
			})
			req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("health status after restart seed = %d, want %d", rr.Code, http.StatusOK)
			}

			if err := restarted.WithTx(context.Background(), func(tx store.Tx) error {
				_, err := tx.ClaimAvailableToken(context.Background(), "T-listen", "N-02", 2, "qa-3")
				return err
			}); err != nil {
				t.Fatalf("claim available token after restart seed: %v", err)
			}
		})
	}
}
