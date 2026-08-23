package records

import (
	"context"

	"boltforge-highstrength-joint-qa/internal/domain"
	"boltforge-highstrength-joint-qa/internal/store"
)

// Service is the evidence-ledger read projection. It reads the append-only
// record streams for a task so the HTTP layer and the arbiter can assemble the
// audit summary and sampling closure without touching persistence internals.
type Service struct {
	store store.Store
}

// NewService wires the evidence ledger against the persistence port.
func NewService(st store.Store) *Service { return &Service{store: st} }

func (s *Service) PairVerifications(taskID string) ([]PairVerification, error) {
	var out []domain.PairVerification
	err := s.store.WithTx(context.Background(), func(tx store.Tx) error {
		var err error
		out, err = tx.ListPairVerifications(context.Background(), taskID)
		return err
	})
	return out, err
}

func (s *Service) TorqueRechecks(taskID string) ([]TorqueCoefficientRun, error) {
	var out []domain.TorqueCoefficientRun
	err := s.store.WithTx(context.Background(), func(tx store.Tx) error {
		var err error
		out, err = tx.ListTorqueRechecks(context.Background(), taskID)
		return err
	})
	return out, err
}

func (s *Service) Tightenings(taskID string) ([]TighteningRecord, error) {
	var out []domain.TighteningRecord
	err := s.store.WithTx(context.Background(), func(tx store.Tx) error {
		var err error
		out, err = tx.ListTightenings(context.Background(), taskID)
		return err
	})
	return out, err
}

func (s *Service) SamplingResults(taskID string) ([]SamplingResult, error) {
	var out []domain.SamplingResult
	err := s.store.WithTx(context.Background(), func(tx store.Tx) error {
		var err error
		out, err = tx.ListSamplingResults(context.Background(), taskID)
		return err
	})
	return out, err
}

// SamplingClosure reports whether every sampled bolt has a single valid
// conclusion, returning the sorted list of missing sampled bolts.
func SamplingClosure(sampling []domain.SamplingResult, set []domain.SamplingRef) []domain.Reason {
	var reasons []domain.Reason
	for _, ref := range set {
		found := false
		for _, r := range sampling {
			if r.NodeID == ref.NodeID && r.BoltNo == ref.BoltNo && r.Result == domain.SampleOK {
				found = true
				break
			}
		}
		if !found {
			reasons = append(reasons, domain.Reason{Code: domain.CodeSampleMissing, Node: ref.NodeID, Bolt: ref.BoltNo})
		}
	}
	sortReasons(reasons)
	return reasons
}

func sortReasons(rs []domain.Reason) {
	for i := 1; i < len(rs); i++ {
		for j := i; j > 0 && lessReason(rs[j], rs[j-1]); j-- {
			rs[j], rs[j-1] = rs[j-1], rs[j]
		}
	}
}

func lessReason(a, b domain.Reason) bool {
	if a.Node != b.Node {
		return a.Node < b.Node
	}
	return a.Bolt < b.Bolt
}
