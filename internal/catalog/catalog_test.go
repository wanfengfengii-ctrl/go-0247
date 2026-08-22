package catalog_test

import (
	"testing"

	"boltforge-highstrength-joint-qa/internal/catalog"
)

func TestReferenceCatalogFixedData(t *testing.T) {
	cat := catalog.Reference()

	nodes := cat.Nodes()
	if len(nodes) != 2 {
		t.Fatalf("nodes = %d, want 2", len(nodes))
	}

	// Bolt numbers must be a contiguous sequence starting at one.
	n := nodes[1] // N-02
	if n.NodeID != "N-02" {
		t.Fatalf("node id = %q, want N-02", n.NodeID)
	}
	if len(n.Bolts) != 3 {
		t.Fatalf("N-02 bolts = %d, want 3", len(n.Bolts))
	}
	for i, b := range n.Bolts {
		if b.BoltNo != i+1 {
			t.Fatalf("bolt %d = %d, want contiguous %d", i, b.BoltNo, i+1)
		}
	}

	if got := cat.Batches(catalog.BoltBatch); len(got) != 1 || got[0].BatchNo != "B-001" {
		t.Fatalf("bolt batches = %+v", got)
	}
	if got := cat.CompatibilityRules(); len(got) != 1 || got[0].SocketSpec != "M24" {
		t.Fatalf("rules = %+v", got)
	}
	if got := cat.Devices(); len(got) != 1 || got[0].DeviceID != "TD-001" {
		t.Fatalf("devices = %+v", got)
	}
	if got := cat.Personnel(); len(got) != 2 {
		t.Fatalf("personnel = %d, want 2", len(got))
	}
}

func TestNodeByIDAndDeviceByID(t *testing.T) {
	cat := catalog.Reference()

	if _, ok := cat.NodeByID("N-01"); !ok {
		t.Fatal("expected N-01 to be found")
	}
	if _, ok := cat.NodeByID("N-99"); ok {
		t.Fatal("expected N-99 to be absent")
	}
	if _, ok := cat.DeviceByID("TD-001"); !ok {
		t.Fatal("expected TD-001 to be found")
	}
}

func TestNodesReturnsCopyOfSlice(t *testing.T) {
	cat := catalog.Reference()
	first := cat.Nodes()
	if len(first) != 2 {
		t.Fatal("expected 2 nodes")
	}
	// Appending to the returned slice must not change the stored catalogue.
	_ = append(first, first[0])
	second := cat.Nodes()
	if len(second) != 2 {
		t.Fatalf("catalogue mutated by caller: nodes = %d", len(second))
	}
}
