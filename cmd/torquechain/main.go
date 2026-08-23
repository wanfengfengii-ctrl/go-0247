// Command torquechain is the executable entry point for the TorqueChain
// high-strength bolt inspection service. It wires the reference catalogue, the
// SQLite-WAL store, the scripted device adapter and every domain service into
// the HTTP API and starts the JSON server.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

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

func main() {
	addr := os.Getenv("TORQUECHAIN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	dbPath := os.Getenv("TORQUECHAIN_DB")
	if dbPath == "" {
		dbPath = "torquechain.db"
	}

	st, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	cat := catalog.Reference()
	if err := seedTokens(st); err != nil {
		log.Fatalf("seed tokens: %v", err)
	}

	clock := domain.SystemClock{}
	dev := device.NewScripted(nil) // normal, deterministic device behaviour

	tasks := task.NewService(st, cat, dev, clock)
	ledgerSvc := ledger.NewService(st, clock)
	recordsSvc := records.NewService(st)
	arbiterSvc := arbiter.NewService(st, clock)

	h := api.NewHandler(api.Deps{
		Catalog: cat,
		Tasks:   tasks,
		Ledger:  ledgerSvc,
		Records: recordsSvc,
		Arbiter: arbiterSvc,
		Store:   st,
		Clock:   clock,
	})

	log.Printf("torquechain listening on %s (db=%s)", addr, dbPath)
	if err := http.ListenAndServe(addr, h); err != nil {
		log.Fatalf("server: %v", err)
	}
}

// seedTokens inserts a reference pool of connection tokens so the pair
// verification flow has physical connection pairs to consume.
func seedTokens(st store.Store) error {
	tokens := make([]domain.ConnectionToken, 0, 10)
	for i := 1; i <= 10; i++ {
		tokens = append(tokens, domain.ConnectionToken{
			TokenID:      tokID(i),
			BatchSummary: "B-001/N-001/W-001",
			Revision:     1,
		})
	}
	return ledger.SeedTokens(context.Background(), st, tokens)
}

func tokID(i int) string { return fmt.Sprintf("TOK-%03d", i) }
