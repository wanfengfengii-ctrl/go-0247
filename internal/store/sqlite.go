package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // register the "sqlite" driver

	"boltforge-highstrength-joint-qa/internal/domain"
)

// SQLite is the concrete SQLite-WAL implementation of Store. It opens the
// database in WAL mode, runs the idempotent migration and performs startup
// recovery. All domain writes flow through WithTx so a failed operation never
// publishes a partial token, lease, record or status revision.
type SQLite struct {
	db   *sql.DB
	path string
}

// Open opens (or creates) the SQLite database at path, migrates the schema and
// recovers. A single writer connection is used so that concurrent write
// transactions serialize deterministically.
func Open(path string) (*SQLite, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// A single connection guarantees a single writer; WAL keeps readers
	// non-blocking while still serializing writes deterministically.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure sqlite: %w", err)
	}

	s := &SQLite{db: db, path: path}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.Recover(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Path returns the on-disk database path.
func (s *SQLite) Path() string { return s.path }

// Close releases the underlying database handle.
func (s *SQLite) Close() error { return s.db.Close() }

// migrate applies the idempotent DDL and ensures the schema version row exists.
func (s *SQLite) migrate(ctx context.Context) error {
	for _, ddl := range schemaDDL {
		if _, err := s.db.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_version`).Scan(&n); err != nil {
		return fmt.Errorf("migrate: read schema version: %w", err)
	}
	if n == 0 {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO schema_version(version, applied_at_seq, last_commit_seq) VALUES(?, 0, 0)`, schemaVersion); err != nil {
			return fmt.Errorf("migrate: seed schema version: %w", err)
		}
	}
	return nil
}

// Recover performs startup recovery. SQLite WAL already guarantees committed
// transactions survive a crash, so recovery verifies integrity, reclaims
// expired unconsumed leases and re-establishes the commit marker.
func (s *SQLite) Recover(ctx context.Context) error {
	var integrity string
	if err := s.db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return fmt.Errorf("recover: integrity check: %w", err)
	}
	if integrity != "ok" {
		return fmt.Errorf("recover: integrity check failed: %s", integrity)
	}

	// Reclaim expired, unreleased leases so a restarted process never reports a
	// stale active lease.
	if _, err := s.db.ExecContext(ctx, `UPDATE device_leases SET released=1 WHERE released=0 AND expires_at <= ?`, time.Now().Unix()); err != nil {
		return fmt.Errorf("recover: reclaim expired leases: %w", err)
	}

	// Re-establish the last commit marker from the audit stream.
	var lastSeq int64
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM audit_events WHERE committed=1`).Scan(&lastSeq); err != nil {
		return fmt.Errorf("recover: read commit marker: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE schema_version SET last_commit_seq=?`, lastSeq); err != nil {
		return fmt.Errorf("recover: update commit marker: %w", err)
	}
	return nil
}

// WithTx runs fn inside a transaction. A non-nil error rolls back every write.
func (s *SQLite) WithTx(ctx context.Context, fn func(Tx) error) error {
	sqlTx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	t := &tx{Tx: sqlTx}
	if err := fn(t); err != nil {
		_ = sqlTx.Rollback()
		return err
	}
	if err := sqlTx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// tx adapts a *sql.Tx to the typed Tx persistence port.
type tx struct {
	*sql.Tx
}

// --- tasks ------------------------------------------------------------------

func (t *tx) LoadTask(ctx context.Context, taskID string) (*domain.InspectionTask, error) {
	var data []byte
	err := t.QueryRowContext(ctx, `SELECT data FROM tasks WHERE task_id=?`, taskID).Scan(&data)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var tk domain.InspectionTask
	if err := json.Unmarshal(data, &tk); err != nil {
		return nil, fmt.Errorf("decode task: %w", err)
	}
	return &tk, nil
}

func (t *tx) SaveTask(ctx context.Context, tk *domain.InspectionTask, expectRevision domain.Revision) error {
	data, err := json.Marshal(tk)
	if err != nil {
		return err
	}
	res, err := t.ExecContext(ctx,
		`INSERT INTO tasks(task_id, data, status, revision) VALUES(?, ?, ?, ?)
		 ON CONFLICT(task_id) DO UPDATE SET data=excluded.data, status=excluded.status, revision=excluded.revision
		 WHERE tasks.revision = ?`,
		tk.TaskID, data, string(tk.Status), int64(tk.Revision), int64(expectRevision))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.NewError(domain.CodeStaleRevision, "task revision conflict").WithRevision(int64(expectRevision))
	}
	return nil
}

// --- evidence ---------------------------------------------------------------

func (t *tx) AppendPairVerification(ctx context.Context, v domain.PairVerification) error {
	_, err := t.ExecContext(ctx,
		`INSERT INTO pair_verifications(task_id, operation_no, node_id, bolt_no, bolt_batch, nut_batch, washer_batch, grade, socket_spec, result)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		v.TaskID, string(v.OperationNo), v.NodeID, v.BoltNo, v.BoltBatch, v.NutBatch, v.WasherBatch, v.Grade, v.SocketSpec, v.Result)
	return err
}

func (t *tx) AppendTorqueRecheck(ctx context.Context, r domain.TorqueCoefficientRun) error {
	_, err := t.ExecContext(ctx,
		`INSERT INTO torque_rechecks(task_id, operation_no, retry_seq, device_id, calibration_version, coefficient, device_receipt, result)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		r.TaskID, string(r.OperationNo), r.RetrySeq, r.DeviceID, r.CalibrationVersion, r.Coefficient, r.DeviceReceipt, r.Result)
	return err
}

func (t *tx) AppendTightening(ctx context.Context, r domain.TighteningRecord) error {
	_, err := t.ExecContext(ctx,
		`INSERT INTO tightening_records(task_id, operation_no, phase, node_id, bolt_no, torque_nm, angle_deg, recorded_at, lease_id, cursor_before, cursor_after, summary)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.TaskID, string(r.OperationNo), string(r.Phase), r.NodeID, r.BoltNo, int(r.TorqueNm), int(r.AngleDeg), r.RecordedAt.Unix(), r.LeaseID, r.CursorBefore, r.CursorAfter, r.Summary)
	return err
}

func (t *tx) AppendSamplingResult(ctx context.Context, r domain.SamplingResult) error {
	_, err := t.ExecContext(ctx,
		`INSERT INTO sampling_results(task_id, operation_no, node_id, bolt_no, attempt_seq, measured_preload, allowed_min, allowed_max, device_id, person_id, result, evidence_summary, prev_hash)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.TaskID, string(r.OperationNo), r.NodeID, r.BoltNo, r.AttemptSeq, r.MeasuredPreload, r.AllowedMin, r.AllowedMax, r.DeviceID, r.PersonID, string(r.Result), r.EvidenceSummary, r.PrevHash)
	return err
}

func (t *tx) ListPairVerifications(ctx context.Context, taskID string) ([]domain.PairVerification, error) {
	rows, err := t.QueryContext(ctx, `SELECT operation_no, node_id, bolt_no, bolt_batch, nut_batch, washer_batch, grade, socket_spec, result FROM pair_verifications WHERE task_id=? ORDER BY node_id, bolt_no, operation_no`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.PairVerification
	for rows.Next() {
		var v domain.PairVerification
		var op string
		if err := rows.Scan(&op, &v.NodeID, &v.BoltNo, &v.BoltBatch, &v.NutBatch, &v.WasherBatch, &v.Grade, &v.SocketSpec, &v.Result); err != nil {
			return nil, err
		}
		v.TaskID = taskID
		v.OperationNo = domain.OperationNo(op)
		out = append(out, v)
	}
	return out, rows.Err()
}

func (t *tx) ListTorqueRechecks(ctx context.Context, taskID string) ([]domain.TorqueCoefficientRun, error) {
	rows, err := t.QueryContext(ctx, `SELECT operation_no, retry_seq, device_id, calibration_version, coefficient, device_receipt, result FROM torque_rechecks WHERE task_id=? ORDER BY retry_seq`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.TorqueCoefficientRun
	for rows.Next() {
		var r domain.TorqueCoefficientRun
		var op string
		if err := rows.Scan(&op, &r.RetrySeq, &r.DeviceID, &r.CalibrationVersion, &r.Coefficient, &r.DeviceReceipt, &r.Result); err != nil {
			return nil, err
		}
		r.TaskID = taskID
		r.OperationNo = domain.OperationNo(op)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (t *tx) ListTightenings(ctx context.Context, taskID string) ([]domain.TighteningRecord, error) {
	rows, err := t.QueryContext(ctx, `SELECT operation_no, phase, node_id, bolt_no, torque_nm, angle_deg, recorded_at, lease_id, cursor_before, cursor_after, summary FROM tightening_records WHERE task_id=? ORDER BY node_id, bolt_no, phase`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.TighteningRecord
	for rows.Next() {
		var r domain.TighteningRecord
		var op string
		var ts int64
		if err := rows.Scan(&op, &r.Phase, &r.NodeID, &r.BoltNo, &r.TorqueNm, &r.AngleDeg, &ts, &r.LeaseID, &r.CursorBefore, &r.CursorAfter, &r.Summary); err != nil {
			return nil, err
		}
		r.TaskID = taskID
		r.OperationNo = domain.OperationNo(op)
		r.RecordedAt = time.Unix(ts, 0)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (t *tx) ListSamplingResults(ctx context.Context, taskID string) ([]domain.SamplingResult, error) {
	rows, err := t.QueryContext(ctx, `SELECT operation_no, node_id, bolt_no, attempt_seq, measured_preload, allowed_min, allowed_max, device_id, person_id, result, evidence_summary, prev_hash FROM sampling_results WHERE task_id=? ORDER BY node_id, bolt_no, attempt_seq`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.SamplingResult
	for rows.Next() {
		var r domain.SamplingResult
		var op string
		if err := rows.Scan(&op, &r.NodeID, &r.BoltNo, &r.AttemptSeq, &r.MeasuredPreload, &r.AllowedMin, &r.AllowedMax, &r.DeviceID, &r.PersonID, &r.Result, &r.EvidenceSummary, &r.PrevHash); err != nil {
			return nil, err
		}
		r.TaskID = taskID
		r.OperationNo = domain.OperationNo(op)
		out = append(out, r)
	}
	return out, rows.Err()
}

// --- connection tokens ------------------------------------------------------

func (t *tx) InsertToken(ctx context.Context, tok domain.ConnectionToken) error {
	_, err := t.ExecContext(ctx,
		`INSERT INTO connection_tokens(token_id, task_id, node_id, bolt_no, batch_summary, consumed, holder, released, revision) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tok.TokenID, tok.TaskID, tok.NodeID, tok.BoltNo, tok.BatchSummary, boolInt(tok.Consumed), tok.Holder, boolInt(tok.Released), int64(tok.Revision))
	return err
}

func (t *tx) ClaimToken(ctx context.Context, tokenID, taskID, nodeID string, boltNo int, holder string) (*domain.ConnectionToken, error) {
	res, err := t.ExecContext(ctx,
		`UPDATE connection_tokens SET task_id=?, node_id=?, bolt_no=?, holder=?, revision=revision+1
		 WHERE token_id=? AND task_id='' AND consumed=0 AND released=0`,
		taskID, nodeID, boltNo, holder, tokenID)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, domain.NewError(domain.CodeTokenBusy, "connection token unavailable or already occupied")
	}
	return t.loadToken(ctx, tokenID)
}

func (t *tx) ReleaseToken(ctx context.Context, taskID, tokenID string) error {
	res, err := t.ExecContext(ctx,
		`UPDATE connection_tokens SET released=1, revision=revision+1
		 WHERE token_id=? AND task_id=? AND consumed=0 AND released=0`,
		tokenID, taskID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.NewError(domain.CodeTokenBusy, "token is consumed or not held by this task")
	}
	return nil
}

func (t *tx) ClaimAvailableToken(ctx context.Context, taskID, nodeID string, boltNo int, holder string) (*domain.ConnectionToken, error) {
	res, err := t.ExecContext(ctx,
		`UPDATE connection_tokens SET task_id=?, node_id=?, bolt_no=?, holder=?, revision=revision+1
		 WHERE token_id = (SELECT token_id FROM connection_tokens WHERE task_id='' AND consumed=0 AND released=0 ORDER BY token_id LIMIT 1)
		   AND task_id='' AND consumed=0 AND released=0`,
		taskID, nodeID, boltNo, holder)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, domain.NewError(domain.CodeTokenBusy, "no available connection token")
	}
	var tokenID string
	if err := t.QueryRowContext(ctx, `SELECT token_id FROM connection_tokens WHERE task_id=? AND node_id=? AND bolt_no=? ORDER BY token_id LIMIT 1`, taskID, nodeID, boltNo).Scan(&tokenID); err != nil {
		return nil, err
	}
	return t.loadToken(ctx, tokenID)
}

func (t *tx) ConsumeToken(ctx context.Context, taskID, nodeID string, boltNo int) error {
	res, err := t.ExecContext(ctx,
		`UPDATE connection_tokens SET consumed=1, revision=revision+1
		 WHERE task_id=? AND node_id=? AND bolt_no=? AND consumed=0`,
		taskID, nodeID, boltNo)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.NewError(domain.CodeTokenBusy, "no unconsumed token bound to this bolt")
	}
	return nil
}

func (t *tx) loadToken(ctx context.Context, tokenID string) (*domain.ConnectionToken, error) {
	var tok domain.ConnectionToken
	var consumed, released int
	err := t.QueryRowContext(ctx, `SELECT token_id, task_id, node_id, bolt_no, batch_summary, consumed, holder, released, revision FROM connection_tokens WHERE token_id=?`, tokenID).
		Scan(&tok.TokenID, &tok.TaskID, &tok.NodeID, &tok.BoltNo, &tok.BatchSummary, &consumed, &tok.Holder, &released, &tok.Revision)
	if err != nil {
		return nil, err
	}
	tok.Consumed = consumed != 0
	tok.Released = released != 0
	return &tok, nil
}

// --- device leases ----------------------------------------------------------

func (t *tx) InsertLease(ctx context.Context, l domain.DeviceLease) error {
	_, err := t.ExecContext(ctx,
		`INSERT INTO device_leases(lease_id, device_id, task_id, task_generation, holder, calibration_version, revision, expires_at, released) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		l.LeaseID, l.DeviceID, l.TaskID, int64(l.TaskGeneration), l.Holder, l.CalibrationVersion, int64(l.Revision), l.ExpiresAt.Unix(), boolInt(l.Released))
	return err
}

func (t *tx) ClaimLease(ctx context.Context, leaseID, deviceID, taskID string, generation domain.Generation, holder, calVersion string, expiresAt time.Time, now time.Time) (*domain.DeviceLease, error) {
	// Reject if any active lease already exists for the device.
	var active int
	if err := t.QueryRowContext(ctx, `SELECT COUNT(*) FROM device_leases WHERE device_id=? AND released=0 AND expires_at > ?`, deviceID, now.Unix()).Scan(&active); err != nil {
		return nil, err
	}
	if active > 0 {
		return nil, domain.NewError(domain.CodeDeviceBusy, "torque device already leased")
	}
	_, err := t.ExecContext(ctx,
		`INSERT INTO device_leases(lease_id, device_id, task_id, task_generation, holder, calibration_version, revision, expires_at, released) VALUES(?, ?, ?, ?, ?, ?, 1, ?, 0)`,
		leaseID, deviceID, taskID, int64(generation), holder, calVersion, expiresAt.Unix())
	if err != nil {
		return nil, err
	}
	return t.loadLease(ctx, leaseID)
}

func (t *tx) ReleaseLease(ctx context.Context, taskID, leaseID string) error {
	res, err := t.ExecContext(ctx,
		`UPDATE device_leases SET released=1 WHERE lease_id=? AND task_id=? AND released=0`, leaseID, taskID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.NewError(domain.CodeDeviceBusy, "lease not held by this task or already released")
	}
	return nil
}

func (t *tx) GetLease(ctx context.Context, leaseID string) (*domain.DeviceLease, error) {
	return t.loadLease(ctx, leaseID)
}

func (t *tx) ListLeases(ctx context.Context, taskID string) ([]domain.DeviceLease, error) {
	rows, err := t.QueryContext(ctx, `SELECT lease_id, device_id, task_id, task_generation, holder, calibration_version, revision, expires_at, released FROM device_leases WHERE task_id=? ORDER BY lease_id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.DeviceLease
	for rows.Next() {
		var l domain.DeviceLease
		var released int
		var exp int64
		if err := rows.Scan(&l.LeaseID, &l.DeviceID, &l.TaskID, &l.TaskGeneration, &l.Holder, &l.CalibrationVersion, &l.Revision, &exp, &released); err != nil {
			return nil, err
		}
		l.ExpiresAt = time.Unix(exp, 0)
		l.Released = released != 0
		out = append(out, l)
	}
	return out, rows.Err()
}

func (t *tx) ReclaimExpiredLeases(ctx context.Context, now time.Time) (int, error) {
	res, err := t.ExecContext(ctx, `UPDATE device_leases SET released=1 WHERE released=0 AND expires_at <= ?`, now.Unix())
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

func (t *tx) loadLease(ctx context.Context, leaseID string) (*domain.DeviceLease, error) {
	var l domain.DeviceLease
	var released int
	var exp int64
	err := t.QueryRowContext(ctx, `SELECT lease_id, device_id, task_id, task_generation, holder, calibration_version, revision, expires_at, released FROM device_leases WHERE lease_id=?`, leaseID).
		Scan(&l.LeaseID, &l.DeviceID, &l.TaskID, &l.TaskGeneration, &l.Holder, &l.CalibrationVersion, &l.Revision, &exp, &released)
	if err != nil {
		return nil, err
	}
	l.ExpiresAt = time.Unix(exp, 0)
	l.Released = released != 0
	return &l, nil
}

// --- reviews & decisions ----------------------------------------------------

func (t *tx) PutReview(ctx context.Context, r domain.Review) error {
	_, err := t.ExecContext(ctx,
		`INSERT INTO reviews(task_id, seat, person_id, qualification_version, task_summary, evidence_summary, signed_at) VALUES(?, ?, ?, ?, ?, ?, ?)`,
		r.TaskID, int(r.Seat), r.PersonID, r.QualificationVersion, r.TaskSummary, r.EvidenceSummary, r.SignedAt.Unix())
	return err
}

func (t *tx) ListReviews(ctx context.Context, taskID string) ([]domain.Review, error) {
	rows, err := t.QueryContext(ctx, `SELECT seat, person_id, qualification_version, task_summary, evidence_summary, signed_at FROM reviews WHERE task_id=? ORDER BY seat`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Review
	for rows.Next() {
		var r domain.Review
		var ts int64
		if err := rows.Scan(&r.Seat, &r.PersonID, &r.QualificationVersion, &r.TaskSummary, &r.EvidenceSummary, &ts); err != nil {
			return nil, err
		}
		r.TaskID = taskID
		r.SignedAt = time.Unix(ts, 0)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (t *tx) PutDecision(ctx context.Context, d domain.FinalDecision) error {
	_, err := t.ExecContext(ctx,
		`INSERT INTO final_decisions(task_id, type, winning_operation, reason_summary, credential, submitted_at) VALUES(?, ?, ?, ?, ?, ?)`,
		d.TaskID, string(d.Type), string(d.WinningOperation), d.ReasonSummary, d.Credential, d.SubmittedAt.Unix())
	return err
}

func (t *tx) GetDecision(ctx context.Context, taskID string) (*domain.FinalDecision, error) {
	var d domain.FinalDecision
	var op string
	var ts int64
	err := t.QueryRowContext(ctx, `SELECT type, winning_operation, reason_summary, credential, submitted_at FROM final_decisions WHERE task_id=?`, taskID).
		Scan(&d.Type, &op, &d.ReasonSummary, &d.Credential, &ts)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.TaskID = taskID
	d.WinningOperation = domain.OperationNo(op)
	d.SubmittedAt = time.Unix(ts, 0)
	return &d, nil
}

// --- idempotency & audit ----------------------------------------------------

func (t *tx) GetIdempotency(ctx context.Context, op domain.OperationNo, taskID string) (IdempotencyEntry, bool, error) {
	var e IdempotencyEntry
	var opNo string
	err := t.QueryRowContext(ctx, `SELECT operation_no, digest, result FROM idempotency WHERE operation_no=? AND task_id=?`, string(op), taskID).
		Scan(&opNo, &e.Digest, &e.Result)
	if err == sql.ErrNoRows {
		return IdempotencyEntry{}, false, nil
	}
	if err != nil {
		return IdempotencyEntry{}, false, err
	}
	e.OperationNo = domain.OperationNo(opNo)
	e.TaskID = taskID
	return e, true, nil
}

func (t *tx) PutIdempotency(ctx context.Context, e IdempotencyEntry) error {
	_, err := t.ExecContext(ctx,
		`INSERT INTO idempotency(operation_no, task_id, digest, result) VALUES(?, ?, ?, ?)`,
		string(e.OperationNo), e.TaskID, e.Digest, e.Result)
	return err
}

func (t *tx) AppendAudit(ctx context.Context, e AuditEvent) error {
	res, err := t.ExecContext(ctx,
		`INSERT INTO audit_events(task_id, operation_no, kind, content_digest, committed) VALUES(?, ?, ?, ?, ?)`,
		e.TaskID, string(e.Operation), e.Kind, e.ContentDigest, boolInt(e.Committed))
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	e.Seq = id
	return nil
}

func (t *tx) ListAudit(ctx context.Context, taskID string) ([]AuditEvent, error) {
	rows, err := t.QueryContext(ctx, `SELECT seq, task_id, operation_no, kind, content_digest, committed FROM audit_events WHERE task_id=? ORDER BY seq`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEvent
	for rows.Next() {
		var e AuditEvent
		var op string
		var committed int
		if err := rows.Scan(&e.Seq, &e.TaskID, &op, &e.Kind, &e.ContentDigest, &committed); err != nil {
			return nil, err
		}
		e.Operation = domain.OperationNo(op)
		e.Committed = committed != 0
		out = append(out, e)
	}
	return out, rows.Err()
}

func (t *tx) LoadSchemaVersion(ctx context.Context) (SchemaVersion, error) {
	var v SchemaVersion
	err := t.QueryRowContext(ctx, `SELECT version, applied_at_seq, last_commit_seq FROM schema_version LIMIT 1`).Scan(&v.Version, &v.AppliedAtSeq, &v.LastCommitSeq)
	return v, err
}

func (t *tx) SaveSchemaVersion(ctx context.Context, v SchemaVersion) error {
	_, err := t.ExecContext(ctx, `UPDATE schema_version SET version=?, applied_at_seq=?, last_commit_seq=?`, v.Version, v.AppliedAtSeq, v.LastCommitSeq)
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
