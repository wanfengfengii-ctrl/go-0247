package ledger_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"boltforge-highstrength-joint-qa/internal/domain"
	"boltforge-highstrength-joint-qa/internal/ledger"
	"boltforge-highstrength-joint-qa/internal/store"
)

func newService(t *testing.T) (*store.SQLite, *ledger.Service) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, ledger.NewService(s, domain.NewFakeClock(time.Unix(1700000000, 0)))
}

func TestConcurrentTokenClaimSingleWinner(t *testing.T) {
	s, svc := newService(t)
	if err := ledger.SeedTokens(context.Background(), s, []domain.ConnectionToken{{TokenID: "TOK-1", BatchSummary: "b/n/w", Revision: 1}}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var wins, losses int
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			<-start
			_, err := svc.ClaimToken(ledger.ClaimTokenRequest{
				TaskID: "T-" + string(rune('A'+n)), OperationNo: domain.OperationNo("op"), OperatorID: "op",
				TokenID: "TOK-1", NodeID: "N-01", BoltNo: 1,
			})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				wins++
			} else if domain.IsCode(err, domain.CodeTokenBusy) {
				losses++
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if wins != 1 || losses != 1 {
		t.Fatalf("wins=%d losses=%d, want 1/1", wins, losses)
	}
}

func TestConcurrentDeviceLeaseSingleWinner(t *testing.T) {
	s, svc := newService(t)
	_ = s
	var wg sync.WaitGroup
	var mu sync.Mutex
	var wins, losses int
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			<-start
			_, err := svc.ClaimLease(ledger.ClaimLeaseRequest{
				TaskID: "T-" + string(rune('A'+n)), OperationNo: domain.OperationNo("op"), OperatorID: "op",
				DeviceID: "TD-001", Generation: domain.Generation(n + 1), CalibrationVersion: "CAL-1", HoldSeconds: 3600,
			})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				wins++
			} else if domain.IsCode(err, domain.CodeDeviceBusy) {
				losses++
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if wins != 1 || losses != 1 {
		t.Fatalf("wins=%d losses=%d, want 1/1", wins, losses)
	}
}

func TestReleaseUnconsumedToken(t *testing.T) {
	s, svc := newService(t)
	if err := ledger.SeedTokens(context.Background(), s, []domain.ConnectionToken{{TokenID: "TOK-1", BatchSummary: "b/n/w", Revision: 1}}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := svc.ClaimToken(ledger.ClaimTokenRequest{TaskID: "T-1", OperationNo: "op", OperatorID: "op", TokenID: "TOK-1", NodeID: "N-01", BoltNo: 1}); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := svc.ReleaseToken("T-1", "TOK-1", "op2"); err != nil {
		t.Fatalf("release: %v", err)
	}
	// A second release of the already-released token fails.
	if err := svc.ReleaseToken("T-1", "TOK-1", "op3"); !domain.IsCode(err, domain.CodeTokenBusy) {
		t.Fatalf("want TOKEN_BUSY on double release, got %v", err)
	}
}
