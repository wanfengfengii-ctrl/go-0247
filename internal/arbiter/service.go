package arbiter

import (
	"context"
	"time"

	"boltforge-highstrength-joint-qa/internal/domain"
	"boltforge-highstrength-joint-qa/internal/records"
	"boltforge-highstrength-joint-qa/internal/store"
)

// Service is the concrete review/finalization arbiter. It enforces the
// two-seat independent review, computes the signable precondition and runs the
// single-winner terminal competition among sign, quarantine and cancel.
type Service struct {
	store store.Store
	clock domain.Clock
}

// NewService wires the arbiter against the persistence and clock ports.
func NewService(st store.Store, clock domain.Clock) *Service {
	return &Service{store: st, clock: clock}
}

// SubmitReview records one independent review seat. The reviewer must be
// qualified, distinct from the other seat and confirm the frozen task summary.
// When both seats are filled and sampling is closed the task becomes signable.
func (s *Service) SubmitReview(r ReviewRequest, personQualVersion, currentQualVersion string) error {
	return s.store.WithTx(context.Background(), func(tx store.Tx) error {
		ctx := context.Background()
		t, err := tx.LoadTask(ctx, r.TaskID)
		if err != nil {
			return err
		}
		if t == nil {
			return domain.NewError(domain.CodeNodeNotFound, "task not found")
		}
		if t.Status.IsTerminal() {
			return domain.NewError(domain.CodeTerminalState, "task is in a terminal state")
		}
		if t.Status != domain.StatusSamplingReview && t.Status != domain.StatusSignable {
			return domain.NewError(domain.CodeInvalidPhase, "review requires SAMPLING_REVIEW or SIGNABLE state")
		}
		if r.Seat != domain.SeatFirst && r.Seat != domain.SeatSecond {
			return domain.NewError(domain.CodeInvalidPhase, "review seat must be first or second")
		}
		if personQualVersion != currentQualVersion {
			return domain.NewError(domain.CodeReviewerNotQualified, "reviewer qualification version is not current")
		}
		if r.TaskSummary != t.LockSummary {
			return domain.NewError(domain.CodeSummaryMismatch, "review summary does not match the frozen task summary")
		}

		existing, err := tx.ListReviews(ctx, r.TaskID)
		if err != nil {
			return err
		}
		for _, e := range existing {
			if e.PersonID == r.PersonID {
				return domain.NewError(domain.CodeReviewerOverlap, "same reviewer cannot hold both seats")
			}
			if e.Seat == r.Seat {
				return domain.NewError(domain.CodeReviewerOverlap, "review seat already filled")
			}
		}

		review := domain.Review{
			TaskID: r.TaskID, Seat: r.Seat, PersonID: r.PersonID,
			QualificationVersion: personQualVersion, TaskSummary: r.TaskSummary,
			EvidenceSummary: t.LockSummary, SignedAt: s.clock.Now(),
		}
		if err := tx.PutReview(ctx, review); err != nil {
			return err
		}
		tx.AppendAudit(ctx, store.AuditEvent{TaskID: r.TaskID, Operation: r.OperationNo, Kind: "REVIEW", ContentDigest: domain.Digest(review), Committed: true})

		// Advance to signable when both seats are filled and sampling is closed.
		all, err := tx.ListReviews(ctx, r.TaskID)
		if err != nil {
			return err
		}
		if len(all) >= 2 {
			sampling, err := tx.ListSamplingResults(ctx, r.TaskID)
			if err != nil {
				return err
			}
			if len(records.SamplingClosure(sampling, t.SamplingSet)) == 0 {
				t.Status = domain.StatusSignable
				t.Revision++
				if err := tx.SaveTask(ctx, t, t.Revision-1); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// Finalize runs the terminal competition. Only one decision wins; every loser
// receives a stable FINALIZED_CONFLICT or TERMINAL_STATE error.
func (s *Service) Finalize(req FinalizeRequest) (*domain.FinalDecision, error) {
	var out *domain.FinalDecision
	err := s.store.WithTx(context.Background(), func(tx store.Tx) error {
		ctx := context.Background()
		t, err := tx.LoadTask(ctx, req.TaskID)
		if err != nil {
			return err
		}
		if t == nil {
			return domain.NewError(domain.CodeNodeNotFound, "task not found")
		}
		if t.Status.IsTerminal() {
			return domain.NewError(domain.CodeTerminalState, "task is in a terminal state")
		}
		if !s.allowed(req.Type, t.Status) {
			return domain.NewError(domain.CodeFinalizedConflict, "decision not allowed from current state")
		}
		if req.Type == domain.FinalSign {
			if err := s.verifySignable(ctx, tx, t); err != nil {
				return err
			}
		}

		decision := domain.FinalDecision{
			TaskID: req.TaskID, Type: req.Type, WinningOperation: req.OperationNo,
			ReasonSummary: s.reasonSummary(req.Type), SubmittedAt: s.clock.Now(),
		}
		decision.Credential = "CRED-" + domain.Digest(decision)[:16]

		oldRev := t.Revision
		t.Status = terminalStatus(req.Type)
		t.Revision++
		t.Credential = decision.Credential
		if err := tx.SaveTask(ctx, t, oldRev); err != nil {
			return domain.NewError(domain.CodeFinalizedConflict, "terminal decision already taken")
		}
		if err := tx.PutDecision(ctx, decision); err != nil {
			return err
		}
		tx.AppendAudit(ctx, store.AuditEvent{TaskID: req.TaskID, Operation: req.OperationNo, Kind: "FINALIZE_" + string(req.Type), ContentDigest: decision.Credential, Committed: true})
		out = &decision
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) allowed(ft domain.FinalType, st domain.Status) bool {
	switch ft {
	case domain.FinalSign:
		return st == domain.StatusSignable
	case domain.FinalQuarantine:
		return st == domain.StatusSamplingReview || st == domain.StatusSignable
	case domain.FinalCancel:
		return st == domain.StatusSignable
	default:
		return false
	}
}

func terminalStatus(ft domain.FinalType) domain.Status {
	switch ft {
	case domain.FinalSign:
		return domain.StatusSigned
	case domain.FinalQuarantine:
		return domain.StatusQuarantined
	default:
		return domain.StatusCancelled
	}
}

func (s *Service) reasonSummary(ft domain.FinalType) string {
	switch ft {
	case domain.FinalSign:
		return "all nodes passed, sampling closed, dual review complete, no active lease"
	case domain.FinalQuarantine:
		return "sampling or review failure isolated for rework"
	default:
		return "task cancelled"
	}
}

// verifySignable re-checks the sign precondition inside the competition
// transaction so a racing sampling result or lease cannot slip through. Sign
// requires closed sampling, two independent reviews and no active device
// lease: a torque device still leased to the task means fieldwork is not
// surrendered and no handover credential may be issued.
func (s *Service) verifySignable(ctx context.Context, tx store.Tx, t *domain.InspectionTask) error {
	sampling, err := tx.ListSamplingResults(ctx, t.TaskID)
	if err != nil {
		return err
	}
	if missing := records.SamplingClosure(sampling, t.SamplingSet); len(missing) > 0 {
		return domain.NewError(domain.CodeFinalizedConflict, "sampling not closed").
			WithReasons(missing)
	}
	reviews, err := tx.ListReviews(ctx, t.TaskID)
	if err != nil {
		return err
	}
	if len(reviews) < 2 {
		return domain.NewError(domain.CodeFinalizedConflict, "dual review incomplete")
	}
	leases, err := tx.ListLeases(ctx, t.TaskID)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	var active []domain.Reason
	for _, l := range leases {
		if l.Released || l.IsExpired(now) {
			continue
		}
		active = append(active, domain.Reason{Code: domain.CodeDeviceBusy, Node: l.DeviceID})
	}
	if len(active) > 0 {
		return domain.NewError(domain.CodeFinalizedConflict, "active device lease prevents sign-off").
			WithReasons(active)
	}
	return nil
}

// Now is exposed for tests that need the arbiter's clock reference.
func (s *Service) Now() time.Time { return s.clock.Now() }
