package catalog

import (
	"sync"

	"boltforge-highstrength-joint-qa/internal/domain"
)

// Memory is a mutable in-memory implementation of Catalog. The reference
// catalogue is fixed for the executable, but tests may construct their own
// Memory and call Revise to bump revisions and mutate entries, proving that a
// locked task snapshot never changes after a catalogue edit.
type Memory struct {
	mu        sync.RWMutex
	revision  domain.Revision
	curQual   string
	nodes     []ComponentNode
	batches   map[BatchKind][]Batch
	rules     []CompatibilityRule
	devices   []TorqueDevice
	personnel []Personnel
}

// NewMemory builds a Memory catalogue from the supplied fixed entries.
func NewMemory(nodes []ComponentNode, batches map[BatchKind][]Batch, rules []CompatibilityRule, devices []TorqueDevice, personnel []Personnel) *Memory {
	return &Memory{
		revision:  1,
		curQual:   "Q-3",
		nodes:     nodes,
		batches:   batches,
		rules:     rules,
		devices:   devices,
		personnel: personnel,
	}
}

func (m *Memory) Revision() domain.Revision {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.revision
}

func (m *Memory) Nodes() []ComponentNode {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]ComponentNode(nil), m.nodes...)
}

func (m *Memory) Batches(kind BatchKind) []Batch {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]Batch(nil), m.batches[kind]...)
}

func (m *Memory) CompatibilityRules() []CompatibilityRule {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]CompatibilityRule(nil), m.rules...)
}

func (m *Memory) Devices() []TorqueDevice {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]TorqueDevice(nil), m.devices...)
}

func (m *Memory) Personnel() []Personnel {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]Personnel(nil), m.personnel...)
}

func (m *Memory) NodeByID(id string) (ComponentNode, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, n := range m.nodes {
		if n.NodeID == id {
			return n, true
		}
	}
	return ComponentNode{}, false
}

func (m *Memory) DeviceByID(id string) (TorqueDevice, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, d := range m.devices {
		if d.DeviceID == id {
			return d, true
		}
	}
	return TorqueDevice{}, false
}

func (m *Memory) BatchByNo(kind BatchKind, batchNo string) (Batch, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, b := range m.batches[kind] {
		if b.BatchNo == batchNo {
			return b, true
		}
	}
	return Batch{}, false
}

func (m *Memory) PersonnelByID(id string) (Personnel, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, p := range m.personnel {
		if p.PersonID == id {
			return p, true
		}
	}
	return Personnel{}, false
}

func (m *Memory) CurrentQualificationVersion() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.curQual
}

func (m *Memory) MatchRule(grade, socket string) (CompatibilityRule, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, r := range m.rules {
		if r.Grade == grade && r.SocketSpec == socket {
			return r, true
		}
	}
	return CompatibilityRule{}, false
}

// Revise bumps the global catalogue revision. It is the seam used by tests to
// prove that a locked task snapshot rejects later catalogue edits as stale.
func (m *Memory) Revise() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.revision++
}

// Reference returns the fixed reference catalogue used by the executable entry
// point and by the public tests. It contains two nodes with contiguous bolt
// sequences, a matching triple of batches, one compatibility rule, one device
// and two qualified reviewers.
func Reference() *Memory {
	return NewMemory(
		[]ComponentNode{
			{Project: "P-100", NodeID: "N-01", GeometrySummary: "web/flange joint", Revision: 1,
				Bolts: []BoltSpec{{BoltNo: 1, Grade: "10.9", SocketSpec: "M24"}, {BoltNo: 2, Grade: "10.9", SocketSpec: "M24"}}},
			{Project: "P-100", NodeID: "N-02", GeometrySummary: "splice plate", Revision: 1,
				Bolts: []BoltSpec{{BoltNo: 1, Grade: "10.9", SocketSpec: "M24"}, {BoltNo: 2, Grade: "10.9", SocketSpec: "M24"}, {BoltNo: 3, Grade: "10.9", SocketSpec: "M24"}}},
		},
		map[BatchKind][]Batch{
			BoltBatch:   {{BatchNo: "B-001", Grade: "10.9", Manufacturer: "M-A", InspectionState: "ok", Revision: 1}},
			NutBatch:    {{BatchNo: "N-001", Grade: "10.9", Manufacturer: "M-A", InspectionState: "ok", Revision: 1}},
			WasherBatch: {{BatchNo: "W-001", Grade: "10.9", Manufacturer: "M-A", InspectionState: "ok", Revision: 1}},
		},
		[]CompatibilityRule{{ID: "R-001", BoltBatch: "B-001", NutBatch: "N-001", WasherBatch: "W-001", Grade: "10.9", SocketSpec: "M24", Revision: 1}},
		[]TorqueDevice{{DeviceID: "TD-001", Sockets: []string{"M24"}, CalibrationVersion: "CAL-2026.01", TorqueRange: domain.Range{Min: 100, Max: 900}, AngleRange: domain.Range{Min: 30, Max: 360}, Status: "available", Revision: 1}},
		[]Personnel{
			{PersonID: "P-ALICE", QualificationVersion: "Q-3", Revision: 3},
			{PersonID: "P-BOB", QualificationVersion: "Q-3", Revision: 3},
		},
	)
}
