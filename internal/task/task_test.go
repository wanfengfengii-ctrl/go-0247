package task_test

import (
	"testing"

	"boltforge-highstrength-joint-qa/internal/domain"
	"boltforge-highstrength-joint-qa/internal/task"
)

func TestStatusTransitions(t *testing.T) {
	valid := []struct {
		from, to task.Status
	}{
		{task.StatusPendingLock, task.StatusPairVerification},
		{task.StatusPairVerification, task.StatusTorqueRecheck},
		{task.StatusTorqueRecheck, task.StatusInstallation},
		{task.StatusInstallation, task.StatusSamplingReview},
		{task.StatusSamplingReview, task.StatusSignable},
		{task.StatusSignable, task.StatusSigned},
	}
	for _, c := range valid {
		if !task.CanTransition(c.from, c.to) {
			t.Errorf("expected %s -> %s to be allowed", c.from, c.to)
		}
	}
}

func TestInvalidTransitionsRejected(t *testing.T) {
	invalid := []struct {
		from, to task.Status
	}{
		{task.StatusPendingLock, task.StatusInstallation},
		{task.StatusPairVerification, task.StatusSigned},
		{task.StatusInstallation, task.StatusPendingLock},
		{task.StatusQuarantined, task.StatusSignable},
		{task.StatusSigned, task.StatusSamplingReview},
	}
	for _, c := range invalid {
		if task.CanTransition(c.from, c.to) {
			t.Errorf("expected %s -> %s to be rejected", c.from, c.to)
		}
	}
}

func TestTerminalStatuses(t *testing.T) {
	for _, s := range []task.Status{task.StatusSigned, task.StatusQuarantined, task.StatusCancelled} {
		if !s.IsTerminal() {
			t.Errorf("expected %s to be terminal", s)
		}
	}
	for _, s := range []task.Status{task.StatusPendingLock, task.StatusInstallation, task.StatusSignable} {
		if s.IsTerminal() {
			t.Errorf("expected %s to be non-terminal", s)
		}
	}
}

func TestInitialCursorMustAdvanceContiguously(t *testing.T) {
	node := task.NewTaskNode("N-01", 3) // InitialCursor=1

	if err := task.ValidateInitialOrder(node, 1); err != nil {
		t.Fatalf("bolt 1 should be accepted: %v", err)
	}
	if err := task.ValidateInitialOrder(node, 2); !domain.IsCode(err, domain.CodeSequenceGap) {
		t.Fatalf("bolt 2 should be a sequence gap, got %v", err)
	}
	if err := task.ValidateInitialOrder(node, 4); !domain.IsCode(err, domain.CodeSequenceGap) {
		t.Fatalf("out-of-range bolt should be a sequence gap, got %v", err)
	}
}

func TestFinalOrderRequiresInitialAndPrecedingFinal(t *testing.T) {
	// Bolts 1 and 2 initially tightened (InitialCursor=3), none finally yet.
	node := task.TaskNode{NodeID: "N-01", BoltCount: 3, InitialCursor: 3, FinalCursor: 1}

	// Final bolt 1 is valid (initial done, no preceding bolt).
	if err := task.ValidateFinalOrder(node, 1); err != nil {
		t.Fatalf("final bolt 1 should be valid: %v", err)
	}
	// Final bolt 2 skips bolt 1's final -> sequence gap.
	if err := task.ValidateFinalOrder(node, 2); !domain.IsCode(err, domain.CodeSequenceGap) {
		t.Fatalf("final bolt 2 should be a sequence gap, got %v", err)
	}
	// Final bolt 3 is not initially tightened -> invalid phase.
	if err := task.ValidateFinalOrder(node, 3); !domain.IsCode(err, domain.CodeInvalidPhase) {
		t.Fatalf("final bolt 3 should be invalid phase, got %v", err)
	}
}
