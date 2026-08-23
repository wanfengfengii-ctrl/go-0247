package device

import (
	"context"
	"fmt"
)

// Scripted is a deterministic device adapter driven by a fixed program. Each
// entry is consumed in order; once the program is exhausted the device keeps
// returning normal readings with a monotonically increasing receipt counter.
type Scripted struct {
	program []Outcome
	next    int
	receipt int
}

// NewScripted builds a Scripted adapter from an ordered outcome program.
func NewScripted(program []Outcome) *Scripted {
	return &Scripted{program: append([]Outcome(nil), program...)}
}

// Start applies the next scripted outcome; a failure outcome aborts the trial
// before any reading is produced, mirroring a real device that refuses to
// start.
func (s *Scripted) Start(ctx context.Context, req StartRequest) error {
	return s.consume()
}

// Read returns a deterministic reading. The coefficient is a pure function of
// the receipt counter so repeated executions are byte-identical.
func (s *Scripted) Read(ctx context.Context, req ReadRequest) (Reading, error) {
	if err := s.consume(); err != nil {
		return Reading{}, err
	}
	s.receipt++
	coefficient := 0.11 + float64(s.receipt%5)*0.01
	return Reading{
		Coefficient: coefficient,
		Receipt:     fmt.Sprintf("receipt-%s-%03d", req.DeviceID, s.receipt),
	}, nil
}

// Stop applies the next scripted outcome and otherwise is a no-op.
func (s *Scripted) Stop(ctx context.Context, req StopRequest) error {
	return s.consume()
}

func (s *Scripted) consume() error {
	if s.next < len(s.program) {
		o := s.program[s.next]
		s.next++
		if err := MapOutcome(o); err != nil {
			return err
		}
	}
	return nil
}
