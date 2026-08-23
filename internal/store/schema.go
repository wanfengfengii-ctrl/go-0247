package store

// schemaVersion is the single applied schema revision. Bumping it triggers a
// fresh migration on the next open; the migration DDL is idempotent.
const schemaVersion = 1

// schemaDDL is the full set of CREATE TABLE IF NOT EXISTS statements. Column
// types are intentionally simple (INTEGER/TEXT/REAL) because modernc sqlite is
// dynamically typed and the typed accessors live in the repository methods.
var schemaDDL = []string{
	`CREATE TABLE IF NOT EXISTS schema_version (
		version         INTEGER NOT NULL,
		applied_at_seq  INTEGER NOT NULL,
		last_commit_seq INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS audit_events (
		seq            INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id        TEXT NOT NULL,
		operation_no   TEXT NOT NULL,
		kind           TEXT NOT NULL,
		content_digest TEXT NOT NULL,
		committed      INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE TABLE IF NOT EXISTS idempotency (
		operation_no TEXT NOT NULL,
		task_id      TEXT NOT NULL,
		digest       TEXT NOT NULL,
		result       TEXT NOT NULL,
		PRIMARY KEY (operation_no, task_id)
	)`,
	`CREATE TABLE IF NOT EXISTS tasks (
		task_id  TEXT PRIMARY KEY,
		data     TEXT NOT NULL,
		status   TEXT NOT NULL,
		revision INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS pair_verifications (
		task_id       TEXT NOT NULL,
		operation_no  TEXT NOT NULL,
		node_id       TEXT NOT NULL,
		bolt_no       INTEGER NOT NULL,
		bolt_batch    TEXT NOT NULL,
		nut_batch     TEXT NOT NULL,
		washer_batch  TEXT NOT NULL,
		grade         TEXT NOT NULL,
		socket_spec   TEXT NOT NULL,
		result        TEXT NOT NULL,
		PRIMARY KEY (task_id, operation_no)
	)`,
	`CREATE TABLE IF NOT EXISTS torque_rechecks (
		task_id             TEXT NOT NULL,
		operation_no        TEXT NOT NULL,
		retry_seq           INTEGER NOT NULL,
		device_id           TEXT NOT NULL,
		calibration_version TEXT NOT NULL,
		coefficient         REAL NOT NULL,
		device_receipt      TEXT NOT NULL,
		result              TEXT NOT NULL,
		PRIMARY KEY (task_id, operation_no)
	)`,
	`CREATE TABLE IF NOT EXISTS tightening_records (
		task_id       TEXT NOT NULL,
		operation_no  TEXT NOT NULL,
		phase         TEXT NOT NULL,
		node_id       TEXT NOT NULL,
		bolt_no       INTEGER NOT NULL,
		torque_nm     INTEGER NOT NULL,
		angle_deg     INTEGER NOT NULL,
		recorded_at   INTEGER NOT NULL,
		lease_id      TEXT NOT NULL,
		cursor_before INTEGER NOT NULL,
		cursor_after  INTEGER NOT NULL,
		summary       TEXT NOT NULL,
		PRIMARY KEY (task_id, operation_no)
	)`,
	`CREATE TABLE IF NOT EXISTS sampling_results (
		task_id          TEXT NOT NULL,
		operation_no     TEXT NOT NULL,
		node_id          TEXT NOT NULL,
		bolt_no          INTEGER NOT NULL,
		attempt_seq      INTEGER NOT NULL,
		measured_preload INTEGER NOT NULL,
		allowed_min      INTEGER NOT NULL,
		allowed_max      INTEGER NOT NULL,
		device_id        TEXT NOT NULL,
		person_id        TEXT NOT NULL,
		result           TEXT NOT NULL,
		evidence_summary TEXT NOT NULL,
		prev_hash        TEXT NOT NULL,
		PRIMARY KEY (task_id, operation_no)
	)`,
	`CREATE TABLE IF NOT EXISTS connection_tokens (
		token_id      TEXT PRIMARY KEY,
		task_id       TEXT NOT NULL DEFAULT '',
		node_id       TEXT NOT NULL DEFAULT '',
		bolt_no       INTEGER NOT NULL DEFAULT 0,
		batch_summary TEXT NOT NULL,
		consumed      INTEGER NOT NULL DEFAULT 0,
		holder        TEXT NOT NULL DEFAULT '',
		released      INTEGER NOT NULL DEFAULT 0,
		revision      INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS device_leases (
		lease_id            TEXT PRIMARY KEY,
		device_id           TEXT NOT NULL,
		task_id             TEXT NOT NULL,
		task_generation     INTEGER NOT NULL,
		holder              TEXT NOT NULL,
		calibration_version TEXT NOT NULL,
		revision            INTEGER NOT NULL,
		expires_at          INTEGER NOT NULL,
		released            INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE INDEX IF NOT EXISTS idx_leases_device ON device_leases(device_id, released, expires_at)`,
	`CREATE TABLE IF NOT EXISTS reviews (
		task_id               TEXT NOT NULL,
		seat                  INTEGER NOT NULL,
		person_id             TEXT NOT NULL,
		qualification_version TEXT NOT NULL,
		task_summary          TEXT NOT NULL,
		evidence_summary      TEXT NOT NULL,
		signed_at             INTEGER NOT NULL,
		PRIMARY KEY (task_id, seat)
	)`,
	`CREATE TABLE IF NOT EXISTS final_decisions (
		task_id            TEXT PRIMARY KEY,
		type               TEXT NOT NULL,
		winning_operation  TEXT NOT NULL,
		reason_summary     TEXT NOT NULL,
		credential         TEXT NOT NULL,
		submitted_at       INTEGER NOT NULL
	)`,
}
