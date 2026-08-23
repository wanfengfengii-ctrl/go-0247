package task

import (
	"context"
	"sort"
	"time"

	"boltforge-highstrength-joint-qa/internal/catalog"
	"boltforge-highstrength-joint-qa/internal/device"
	"boltforge-highstrength-joint-qa/internal/domain"
	"boltforge-highstrength-joint-qa/internal/store"
)

// Service is the concrete task aggregate. It is the single authority for the
// lock snapshot, status transitions, cursor advancement and stale-revision
// rejection, and it coordinates every lifecycle operation inside one store
// transaction so a failure never publishes a partial record.
type Service struct {
	store  store.Store
	cat    catalog.Catalog
	device device.Adapter
	clock  domain.Clock
}

// NewService wires the aggregate against the persistence, catalogue, device
// and clock ports.
func NewService(st store.Store, cat catalog.Catalog, dev device.Adapter, clock domain.Clock) *Service {
	return &Service{store: st, cat: cat, device: dev, clock: clock}
}

// Lock freezes the immutable snapshot and creates the task in the pair
// verification state.
func (s *Service) Lock(req LockRequest, cat catalog.Catalog) (*InspectionTask, error) {
	if req.TaskID == "" {
		return nil, domain.NewError(domain.CodeNodeMismatch, "task id required")
	}
	if len(req.NodeIDs) == 0 {
		return nil, domain.NewError(domain.CodeNodeMismatch, "at least one node required")
	}
	snap, err := s.buildSnapshot(req, cat)
	if err != nil {
		return nil, err
	}

	var out *InspectionTask
	err = s.store.WithTx(context.Background(), func(tx store.Tx) error {
		existing, err := tx.LoadTask(context.Background(), req.TaskID)
		if err != nil {
			return err
		}
		if existing != nil {
			return domain.NewError(domain.CodeIdempotencyConflict, "task id already locked")
		}

		tk := &InspectionTask{
			TaskID:           req.TaskID,
			Generation:       req.Generation,
			LockSummary:      domain.Digest(snap),
			Status:           StatusPairVerification,
			Revision:         1,
			TorqueBounds:     req.TorqueBounds,
			RecheckSpec:      req.RecheckSpec,
			DeviceCalVersion: snap.DeviceCalVersion,
			Window:           snap.Window,
			Snapshot:         snap,
		}
		for _, n := range snap.Nodes {
			tk.Nodes = append(tk.Nodes, NewTaskNode(n.NodeID, len(n.Bolts)))
		}
		tk.SamplingSet = append([]SamplingRef(nil), req.SamplingSet...)

		if err := tx.SaveTask(context.Background(), tk, 0); err != nil {
			return err
		}
		tx.AppendAudit(context.Background(), store.AuditEvent{TaskID: tk.TaskID, Operation: req.OperationNo, Kind: "LOCK", ContentDigest: tk.LockSummary, Committed: true})
		out = tk
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// buildSnapshot resolves and freezes every catalogue input for the lock.
func (s *Service) buildSnapshot(req LockRequest, cat catalog.Catalog) (Snapshot, error) {
	dev, ok := cat.DeviceByID(req.DeviceID)
	if !ok {
		return Snapshot{}, domain.NewError(domain.CodeBatchMismatch, "device not found in catalogue")
	}
	snap := Snapshot{
		Project:           "",
		DeviceID:          dev.DeviceID,
		DeviceCalVersion:  dev.CalibrationVersion,
		DeviceTorqueRange: dev.TorqueRange,
		DeviceAngleRange:  dev.AngleRange,
		CatalogRevision:   cat.Revision(),
		TorqueBounds:      req.TorqueBounds,
		RecheckSpec:       req.RecheckSpec,
		Window:            req.Window,
	}
	for _, nodeID := range req.NodeIDs {
		node, ok := cat.NodeByID(nodeID)
		if !ok {
			return Snapshot{}, domain.NewError(domain.CodeNodeNotFound, "node not found in catalogue").WithReason(domain.Reason{Code: domain.CodeNodeNotFound, Node: nodeID})
		}
		ns := NodeSnapshot{NodeID: node.NodeID, GeometrySummary: node.GeometrySummary, Revision: node.Revision}
		if snap.Project == "" {
			snap.Project = node.Project
		}
		for _, b := range node.Bolts {
			rule, ok := cat.MatchRule(b.Grade, b.SocketSpec)
			if !ok {
				return Snapshot{}, domain.NewError(domain.CodePairMismatch, "no compatibility rule for bolt grade/socket")
			}
			ns.Bolts = append(ns.Bolts, BoltSnapshot{
				BoltNo:       b.BoltNo,
				Grade:        b.Grade,
				SocketSpec:   b.SocketSpec,
				BoltBatch:    rule.BoltBatch,
				NutBatch:     rule.NutBatch,
				WasherBatch:  rule.WasherBatch,
				RuleID:       rule.ID,
				RuleRevision: rule.Revision,
			})
		}
		snap.Nodes = append(snap.Nodes, ns)
	}
	// Validate the sampling set references real bolts in the snapshot.
	for _, ref := range req.SamplingSet {
		if !snapHasBolt(snap, ref.NodeID, ref.BoltNo) {
			return Snapshot{}, domain.NewError(domain.CodeSampleMissing, "sampling reference not in locked nodes").
				WithReason(domain.Reason{Code: domain.CodeSampleMissing, Node: ref.NodeID, Bolt: ref.BoltNo})
		}
	}
	return snap, nil
}

func snapHasBolt(snap Snapshot, nodeID string, boltNo int) bool {
	for _, n := range snap.Nodes {
		if n.NodeID != nodeID {
			continue
		}
		for _, b := range n.Bolts {
			if b.BoltNo == boltNo {
				return true
			}
		}
	}
	return false
}

// Get returns the current task aggregate state.
func (s *Service) Get(taskID string) (*InspectionTask, error) {
	var out *InspectionTask
	err := s.store.WithTx(context.Background(), func(tx store.Tx) error {
		t, err := tx.LoadTask(context.Background(), taskID)
		if err != nil {
			return err
		}
		if t == nil {
			return domain.NewError(domain.CodeNodeNotFound, "task not found")
		}
		out = t
		return nil
	})
	return out, err
}

// loadForWrite loads a task for mutation, enforcing the terminal fence and the
// stale-revision rejection before any write is applied.
func (s *Service) loadForWrite(ctx context.Context, tx store.Tx, taskID string, rev domain.Revision) (*InspectionTask, error) {
	t, err := tx.LoadTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, domain.NewError(domain.CodeNodeNotFound, "task not found")
	}
	if t.Status.IsTerminal() {
		return nil, domain.NewError(domain.CodeTerminalState, "task is in a terminal state")
	}
	if rev != t.Revision {
		return nil, domain.NewError(domain.CodeStaleRevision, "task revision is stale").WithRevision(int64(t.Revision))
	}
	return t, nil
}

// checkIdempotent reports whether an identical operation was already committed
// and returns IDEMPOTENCY_CONFLICT when the operation number is reused with
// different content.
func checkIdempotent(ctx context.Context, tx store.Tx, op domain.OperationNo, taskID, digest string) (bool, error) {
	e, ok, err := tx.GetIdempotency(ctx, op, taskID)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	if e.Digest == digest {
		return true, nil
	}
	return false, domain.NewError(domain.CodeIdempotencyConflict, "operation number reused with different content")
}

func recordIdempotent(ctx context.Context, tx store.Tx, op domain.OperationNo, taskID, digest string) error {
	return tx.PutIdempotency(ctx, store.IdempotencyEntry{OperationNo: op, TaskID: taskID, Digest: digest, Result: "{}"})
}

// VerifyPair validates a connection-pair identity and socket against the locked
// snapshot, consumes the bound token and advances the pair cursor.
func (s *Service) VerifyPair(taskID string, rev domain.Revision, r PairVerifyRequest) error {
	digest := domain.Digest(r)
	return s.store.WithTx(context.Background(), func(tx store.Tx) error {
		ctx := context.Background()
		replay, err := checkIdempotent(ctx, tx, r.OperationNo, taskID, digest)
		if err != nil {
			return err
		}
		if replay {
			return nil
		}
		t, err := s.loadForWrite(ctx, tx, taskID, rev)
		if err != nil {
			return err
		}
		if t.Status != StatusPairVerification {
			return domain.NewError(domain.CodeInvalidPhase, "pair verification requires PAIR_VERIFICATION state")
		}
		bolt, ok := s.snapshotBolt(t, r.NodeID, r.BoltNo)
		if !ok {
			return domain.NewError(domain.CodeNodeNotFound, "bolt not in locked snapshot").
				WithReason(domain.Reason{Code: domain.CodeNodeNotFound, Node: r.NodeID, Bolt: r.BoltNo})
		}
		if r.Grade != bolt.Grade {
			return domain.NewError(domain.CodeGradeMismatch, "material grade mismatch").
				WithReason(domain.Reason{Code: domain.CodeGradeMismatch, Node: r.NodeID, Bolt: r.BoltNo})
		}
		if r.SocketSpec != bolt.SocketSpec {
			return domain.NewError(domain.CodeSocketMismatch, "socket specification mismatch").
				WithReason(domain.Reason{Code: domain.CodeSocketMismatch, Node: r.NodeID, Bolt: r.BoltNo})
		}
		if r.BoltBatch != bolt.BoltBatch || r.NutBatch != bolt.NutBatch || r.WasherBatch != bolt.WasherBatch {
			return domain.NewError(domain.CodePairMismatch, "batch triple does not match locked pair").
				WithReason(domain.Reason{Code: domain.CodePairMismatch, Node: r.NodeID, Bolt: r.BoltNo})
		}
		// Duplicate detection: a bolt may be verified only once.
		existing, err := tx.ListPairVerifications(ctx, taskID)
		if err != nil {
			return err
		}
		for _, p := range existing {
			if p.NodeID == r.NodeID && p.BoltNo == r.BoltNo {
				return domain.NewError(domain.CodeDuplicatePair, "pair already verified").
					WithReason(domain.Reason{Code: domain.CodeDuplicatePair, Node: r.NodeID, Bolt: r.BoltNo})
			}
		}
		// Consume the pre-bound token or claim a fresh one, atomically.
		if err := tx.ConsumeToken(ctx, taskID, r.NodeID, r.BoltNo); err != nil {
			if _, err2 := tx.ClaimAvailableToken(ctx, taskID, r.NodeID, r.BoltNo, r.OperatorID); err2 != nil {
				return err2
			}
			if err := tx.ConsumeToken(ctx, taskID, r.NodeID, r.BoltNo); err != nil {
				return err
			}
		}
		rec := domain.PairVerification{
			OperationNo: r.OperationNo, TaskID: taskID, NodeID: r.NodeID, BoltNo: r.BoltNo,
			BoltBatch: r.BoltBatch, NutBatch: r.NutBatch, WasherBatch: r.WasherBatch,
			Grade: r.Grade, SocketSpec: r.SocketSpec, Result: "OK",
		}
		if err := tx.AppendPairVerification(ctx, rec); err != nil {
			return err
		}
		t.Revision++
		if s.allPairsVerified(tx, taskID, t) {
			t.Status = StatusTorqueRecheck
		}
		if err := tx.SaveTask(ctx, t, rev); err != nil {
			return err
		}
		tx.AppendAudit(ctx, store.AuditEvent{TaskID: taskID, Operation: r.OperationNo, Kind: "PAIR_VERIFY", ContentDigest: digest, Committed: true})
		return recordIdempotent(ctx, tx, r.OperationNo, taskID, digest)
	})
}

func (s *Service) snapshotBolt(t *InspectionTask, nodeID string, boltNo int) (BoltSnapshot, bool) {
	for _, n := range t.Snapshot.Nodes {
		if n.NodeID != nodeID {
			continue
		}
		for _, b := range n.Bolts {
			if b.BoltNo == boltNo {
				return b, true
			}
		}
	}
	return BoltSnapshot{}, false
}

// allPairsVerified reports whether every bolt in every node has a verification.
func (s *Service) allPairsVerified(tx store.Tx, taskID string, t *InspectionTask) bool {
	pairs, err := tx.ListPairVerifications(context.Background(), taskID)
	if err != nil {
		return false
	}
	seen := map[string]bool{}
	for _, p := range pairs {
		seen[domain.BoltKey(p.NodeID, p.BoltNo)] = true
	}
	for _, n := range t.Nodes {
		for bolt := 1; bolt <= n.BoltCount; bolt++ {
			if !seen[domain.BoltKey(n.NodeID, bolt)] {
				return false
			}
		}
	}
	return true
}

// RecheckTorque runs one torque-coefficient recheck trial through the scripted
// device. A failure or an out-of-range coefficient commits a pending-retry
// record and surfaces the stable error without advancing the task; a successful
// trial advances the trial count and may move the task into installation.
func (s *Service) RecheckTorque(taskID string, rev domain.Revision, r RecheckRequest) error {
	digest := domain.Digest(r)
	var opErr error
	err := s.store.WithTx(context.Background(), func(tx store.Tx) error {
		ctx := context.Background()
		replay, err := checkIdempotent(ctx, tx, r.OperationNo, taskID, digest)
		if err != nil {
			return err
		}
		if replay {
			return nil
		}
		t, err := s.loadForWrite(ctx, tx, taskID, rev)
		if err != nil {
			return err
		}
		if t.Status != StatusTorqueRecheck {
			return domain.NewError(domain.CodeInvalidPhase, "torque recheck requires TORQUE_RECHECK state")
		}
		prior, err := tx.ListTorqueRechecks(ctx, taskID)
		if err != nil {
			return err
		}
		retrySeq := len(prior) + 1

		reading, derr := s.runTrial(ctx, t, r)
		run := domain.TorqueCoefficientRun{
			OperationNo: r.OperationNo, TaskID: taskID, RetrySeq: retrySeq,
			DeviceID: r.DeviceID, CalibrationVersion: t.DeviceCalVersion,
		}
		switch {
		case derr != nil:
			run.Result = "PENDING_RETRY"
			opErr = derr
		case reading.Coefficient < t.RecheckSpec.CoefficientMin || reading.Coefficient > t.RecheckSpec.CoefficientMax:
			run.Result = "OUT_OF_RANGE"
			run.Coefficient = reading.Coefficient
			run.DeviceReceipt = reading.Receipt
			opErr = domain.NewError(domain.CodeCoefficientOutOfRange, "torque coefficient out of range")
		default:
			run.Result = "OK"
			run.Coefficient = reading.Coefficient
			run.DeviceReceipt = reading.Receipt
		}
		if err := tx.AppendTorqueRecheck(ctx, run); err != nil {
			return err
		}
		t.Revision++
		if run.Result == "OK" && s.successfulTrials(tx, taskID) >= t.RecheckSpec.Trials {
			t.Status = StatusInstallation
		}
		if err := tx.SaveTask(ctx, t, rev); err != nil {
			return err
		}
		tx.AppendAudit(ctx, store.AuditEvent{TaskID: taskID, Operation: r.OperationNo, Kind: "TORQUE_RECHECK", ContentDigest: digest, Committed: true})
		return recordIdempotent(ctx, tx, r.OperationNo, taskID, digest)
	})
	if err != nil {
		return err
	}
	return opErr
}

// runTrial drives the scripted device Start/Read/Stop and returns the reading
// or the mapped device failure.
func (s *Service) runTrial(ctx context.Context, t *InspectionTask, r RecheckRequest) (device.Reading, error) {
	if err := s.device.Start(ctx, device.StartRequest{DeviceID: r.DeviceID, CalibrationVersion: t.DeviceCalVersion}); err != nil {
		return device.Reading{}, err
	}
	reading, err := s.device.Read(ctx, device.ReadRequest{DeviceID: r.DeviceID})
	if err != nil {
		return device.Reading{}, err
	}
	if err := s.device.Stop(ctx, device.StopRequest{DeviceID: r.DeviceID}); err != nil {
		// A failure during Stop is still a device fault (e.g. a dropped
		// connection after the reading). Per the failure boundaries a device
		// rejection, disconnect or calibration lapse only writes a pending-retry
		// record and never an effective trial, so the reading is discarded and
		// the mapped error is propagated to be recorded as PENDING_RETRY.
		return device.Reading{}, err
	}
	return reading, nil
}

func (s *Service) successfulTrials(tx store.Tx, taskID string) int {
	runs, err := tx.ListTorqueRechecks(context.Background(), taskID)
	if err != nil {
		return 0
	}
	n := 0
	for _, r := range runs {
		if r.Result == "OK" {
			n++
		}
	}
	return n
}

// TightenInitial appends an initial tightening record and advances the cursor.
func (s *Service) TightenInitial(taskID string, rev domain.Revision, r TightenRequest) error {
	return s.tighten(taskID, rev, r, domain.PhaseInitial)
}

// TightenFinal appends a final tightening record and advances the cursor,
// transitioning to sampling review once every bolt is finally tightened.
func (s *Service) TightenFinal(taskID string, rev domain.Revision, r TightenRequest) error {
	return s.tighten(taskID, rev, r, domain.PhaseFinal)
}

func (s *Service) tighten(taskID string, rev domain.Revision, r TightenRequest, phase domain.Phase) error {
	digest := domain.Digest(r)
	return s.store.WithTx(context.Background(), func(tx store.Tx) error {
		ctx := context.Background()
		replay, err := checkIdempotent(ctx, tx, r.OperationNo, taskID, digest)
		if err != nil {
			return err
		}
		if replay {
			return nil
		}
		t, err := s.loadForWrite(ctx, tx, taskID, rev)
		if err != nil {
			return err
		}
		if t.Status != StatusInstallation {
			return domain.NewError(domain.CodeInvalidPhase, "tightening requires INSTALLATION_TIGHTENING state")
		}
		node, idx := s.nodeByID(t, r.NodeID)
		if idx < 0 {
			return domain.NewError(domain.CodeNodeNotFound, "node not in task")
		}
		if phase == domain.PhaseInitial {
			if err := ValidateInitialOrder(node, r.BoltNo); err != nil {
				return err
			}
		} else {
			if err := ValidateFinalOrder(node, r.BoltNo); err != nil {
				return err
			}
		}
		// Reading bounds against the locked interval and the device range.
		if !t.TorqueBounds.TorqueRange.Contains(int(r.TorqueNm)) || !t.Snapshot.DeviceTorqueRange.Contains(int(r.TorqueNm)) {
			return domain.NewError(domain.CodeTorqueOutOfRange, "torque reading out of range").
				WithReason(domain.Reason{Code: domain.CodeTorqueOutOfRange, Node: r.NodeID, Bolt: r.BoltNo})
		}
		if !t.TorqueBounds.AngleRange.Contains(int(r.AngleDeg)) || !t.Snapshot.DeviceAngleRange.Contains(int(r.AngleDeg)) {
			return domain.NewError(domain.CodeAngleOutOfRange, "angle reading out of range").
				WithReason(domain.Reason{Code: domain.CodeAngleOutOfRange, Node: r.NodeID, Bolt: r.BoltNo})
		}
		if !t.Window.ContainsUnix(r.RecordedAt) {
			return domain.NewError(domain.CodeTimeWindowViolation, "timestamp outside construction window").
				WithReason(domain.Reason{Code: domain.CodeTimeWindowViolation, Node: r.NodeID, Bolt: r.BoltNo})
		}
		// Lease validity: active, unexpired, matching calibration version.
		if err := s.checkLease(ctx, tx, t, r.LeaseID); err != nil {
			return err
		}

		before := node.InitialCursor
		if phase == domain.PhaseFinal {
			before = node.FinalCursor
		}
		rec := domain.TighteningRecord{
			OperationNo: r.OperationNo, TaskID: taskID, Phase: phase, NodeID: r.NodeID, BoltNo: r.BoltNo,
			TorqueNm: r.TorqueNm, AngleDeg: r.AngleDeg, RecordedAt: time.Unix(r.RecordedAt, 0),
			LeaseID: r.LeaseID, CursorBefore: before, CursorAfter: before + 1,
		}
		rec.Summary = domain.Digest(rec)
		if err := tx.AppendTightening(ctx, rec); err != nil {
			return err
		}
		if phase == domain.PhaseInitial {
			t.Nodes[idx].InitialCursor++
		} else {
			t.Nodes[idx].FinalCursor++
		}
		t.Revision++
		if phase == domain.PhaseFinal && s.allFinalComplete(t) {
			t.Status = StatusSamplingReview
		}
		if err := tx.SaveTask(ctx, t, rev); err != nil {
			return err
		}
		kind := "TIGHTEN_INITIAL"
		if phase == domain.PhaseFinal {
			kind = "TIGHTEN_FINAL"
		}
		tx.AppendAudit(ctx, store.AuditEvent{TaskID: taskID, Operation: r.OperationNo, Kind: kind, ContentDigest: digest, Committed: true})
		return recordIdempotent(ctx, tx, r.OperationNo, taskID, digest)
	})
}

func (s *Service) checkLease(ctx context.Context, tx store.Tx, t *InspectionTask, leaseID string) error {
	if leaseID == "" {
		return domain.NewError(domain.CodeCalibrationExpired, "a valid device lease is required for tightening")
	}
	l, err := tx.GetLease(ctx, leaseID)
	if err != nil {
		return err
	}
	if l == nil || l.TaskID != t.TaskID {
		return domain.NewError(domain.CodeDeviceBusy, "device lease not held by this task")
	}
	if l.Released {
		return domain.NewError(domain.CodeCalibrationExpired, "device lease already released")
	}
	if l.IsExpired(s.clock.Now()) {
		return domain.NewError(domain.CodeCalibrationExpired, "device lease expired")
	}
	if l.CalibrationVersion != t.DeviceCalVersion {
		return domain.NewError(domain.CodeCalibrationExpired, "device calibration version mismatch")
	}
	return nil
}

func (s *Service) nodeByID(t *InspectionTask, nodeID string) (TaskNode, int) {
	for i, n := range t.Nodes {
		if n.NodeID == nodeID {
			return n, i
		}
	}
	return TaskNode{}, -1
}

func (s *Service) allFinalComplete(t *InspectionTask) bool {
	for _, n := range t.Nodes {
		if n.FinalCursor <= n.BoltCount {
			return false
		}
	}
	return true
}

// SubmitSampling records one sampling recheck conclusion for a sampled bolt.
// An out-of-range reading commits the quarantine transition and returns the
// stable SAMPLE_OUT_OF_RANGE error atomically.
func (s *Service) SubmitSampling(taskID string, rev domain.Revision, r SamplingRequest) error {
	digest := domain.Digest(r)
	var opErr error
	err := s.store.WithTx(context.Background(), func(tx store.Tx) error {
		ctx := context.Background()
		replay, err := checkIdempotent(ctx, tx, r.OperationNo, taskID, digest)
		if err != nil {
			return err
		}
		if replay {
			return nil
		}
		t, err := s.loadForWrite(ctx, tx, taskID, rev)
		if err != nil {
			return err
		}
		if t.Status != StatusSamplingReview && t.Status != StatusSignable {
			return domain.NewError(domain.CodeInvalidPhase, "sampling requires SAMPLING_REVIEW state")
		}
		if !s.inSamplingSet(t, r.NodeID, r.BoltNo) {
			return domain.NewError(domain.CodeSampleMissing, "bolt not in the fixed sampling set").
				WithReason(domain.Reason{Code: domain.CodeSampleMissing, Node: r.NodeID, Bolt: r.BoltNo})
		}
		existing, err := tx.ListSamplingResults(ctx, taskID)
		if err != nil {
			return err
		}
		for _, e := range existing {
			if e.NodeID == r.NodeID && e.BoltNo == r.BoltNo && e.Result == domain.SampleOK {
				return domain.NewError(domain.CodeSampleConflict, "valid sampling conclusion already registered").
					WithReason(domain.Reason{Code: domain.CodeSampleConflict, Node: r.NodeID, Bolt: r.BoltNo})
			}
		}
		allowedMin := t.TorqueBounds.DesignPreload * 85 / 100
		allowedMax := t.TorqueBounds.DesignPreload * 115 / 100
		result := domain.SampleOK
		switch {
		case r.MeasuredPreload < allowedMin:
			result = domain.SampleLow
		case r.MeasuredPreload > allowedMax:
			result = domain.SampleHigh
		}
		var prevHash string
		for _, e := range existing {
			if e.NodeID == r.NodeID && e.BoltNo == r.BoltNo {
				prevHash = e.PrevHash
			}
		}
		rec := domain.SamplingResult{
			OperationNo: r.OperationNo, TaskID: taskID, NodeID: r.NodeID, BoltNo: r.BoltNo,
			AttemptSeq: s.nextAttempt(existing, r.NodeID, r.BoltNo), MeasuredPreload: r.MeasuredPreload,
			AllowedMin: allowedMin, AllowedMax: allowedMax, DeviceID: r.DeviceID, PersonID: r.PersonID,
			Result: result, PrevHash: prevHash,
		}
		rec.EvidenceSummary = domain.Digest(rec)
		if err := tx.AppendSamplingResult(ctx, rec); err != nil {
			return err
		}
		t.Revision++
		if result != domain.SampleOK {
			t.Status = StatusQuarantined
			opErr = domain.NewError(domain.CodeSampleOutOfRange, "sampling preload out of range").
				WithReason(domain.Reason{Code: domain.CodeSampleOutOfRange, Node: r.NodeID, Bolt: r.BoltNo})
		}
		if err := tx.SaveTask(ctx, t, rev); err != nil {
			return err
		}
		tx.AppendAudit(ctx, store.AuditEvent{TaskID: taskID, Operation: r.OperationNo, Kind: "SAMPLING", ContentDigest: digest, Committed: true})
		return recordIdempotent(ctx, tx, r.OperationNo, taskID, digest)
	})
	if err != nil {
		return err
	}
	return opErr
}

func (s *Service) inSamplingSet(t *InspectionTask, nodeID string, boltNo int) bool {
	for _, ref := range t.SamplingSet {
		if ref.NodeID == nodeID && ref.BoltNo == boltNo {
			return true
		}
	}
	return false
}

func (s *Service) nextAttempt(results []domain.SamplingResult, nodeID string, boltNo int) int {
	max := 0
	for _, r := range results {
		if r.NodeID == nodeID && r.BoltNo == boltNo && r.AttemptSeq > max {
			max = r.AttemptSeq
		}
	}
	return max + 1
}

// SortedReasons returns the node/bolt-sorted reasons for a task that is not yet
// signable, used by the HTTP GET projection and the arbiter.
func SortedReasons(sampling []domain.SamplingResult, samplingSet []SamplingRef, nodes []TaskNode) []domain.Reason {
	var reasons []domain.Reason
	// Missing valid conclusions.
	for _, ref := range samplingSet {
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
	// Incomplete final tightening.
	for _, n := range nodes {
		if n.FinalCursor <= n.BoltCount {
			reasons = append(reasons, domain.Reason{Code: domain.CodeSequenceGap, Node: n.NodeID, Bolt: n.FinalCursor})
		}
	}
	sort.SliceStable(reasons, func(i, j int) bool {
		if reasons[i].Node != reasons[j].Node {
			return reasons[i].Node < reasons[j].Node
		}
		return reasons[i].Bolt < reasons[j].Bolt
	})
	return reasons
}
