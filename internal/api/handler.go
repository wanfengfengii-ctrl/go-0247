// Package api is the "Go HTTP API" domain component. It exposes the catalog
// queries, task locking, batch verification, device leasing, tightening
// registration, sampling review, sign/quarantine/cancel and audit queries as a
// JSON HTTP surface, together with the unified error envelope and request
// limits. It performs no business adjudication; every decision is delegated to
// the domain ports in Deps.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"boltforge-highstrength-joint-qa/internal/arbiter"
	"boltforge-highstrength-joint-qa/internal/catalog"
	"boltforge-highstrength-joint-qa/internal/domain"
	"boltforge-highstrength-joint-qa/internal/ledger"
	"boltforge-highstrength-joint-qa/internal/records"
	"boltforge-highstrength-joint-qa/internal/store"
	"boltforge-highstrength-joint-qa/internal/task"
)

const (
	// maxBodyBytes is the request body size limit.
	maxBodyBytes = 64 << 10 // 64 KiB
	// maxFields is the maximum number of top-level JSON object fields.
	maxFields = 128
	// maxStringLen is the maximum accepted length of any string field.
	maxStringLen = 4096
)

// Deps bundles the domain ports consumed by the HTTP layer.
type Deps struct {
	Catalog catalog.Catalog
	Tasks   task.TaskOps
	Ledger  ledger.Ledger
	Records records.Records
	Arbiter arbiter.Arbiter
	Store   store.Store
	Clock   domain.Clock
}

// Handler serves the TorqueChain JSON API and the single-page operator UI.
type Handler struct {
	deps Deps
	mux  *http.ServeMux
}

// NewHandler builds the route table from the supplied domain ports.
func NewHandler(d Deps) *Handler {
	h := &Handler{deps: d}
	mux := http.NewServeMux()
	h.register(mux)
	h.mux = mux
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) register(mux *http.ServeMux) {
	mux.HandleFunc("GET /", h.index)
	mux.HandleFunc("GET /v1/health", h.health)
	mux.HandleFunc("GET /v1/catalog/nodes", h.listNodes)
	mux.HandleFunc("GET /v1/catalog/batches", h.listBatches)
	mux.HandleFunc("GET /v1/catalog/devices", h.listDevices)
	mux.HandleFunc("GET /v1/catalog/personnel", h.listPersonnel)

	mux.HandleFunc("POST /v1/tasks", h.lockTask)
	mux.HandleFunc("GET /v1/tasks/{id}", h.getTask)

	mux.HandleFunc("POST /v1/tasks/{id}/pair-verifications", h.pairVerification)
	mux.HandleFunc("POST /v1/tasks/{id}/torque-rechecks", h.torqueRecheck)
	mux.HandleFunc("POST /v1/tasks/{id}/tightening/initial", h.initialTightening)
	mux.HandleFunc("POST /v1/tasks/{id}/tightening/final", h.finalTightening)
	mux.HandleFunc("POST /v1/tasks/{id}/sampling-results", h.samplingResult)

	mux.HandleFunc("POST /v1/tasks/{id}/leases/claim", h.claimLease)
	mux.HandleFunc("POST /v1/tasks/{id}/leases/release", h.releaseLease)

	mux.HandleFunc("POST /v1/tasks/{id}/reviews", h.submitReview)
	mux.HandleFunc("POST /v1/tasks/{id}/finalize/sign", h.finalizeSign)
	mux.HandleFunc("POST /v1/tasks/{id}/finalize/quarantine", h.finalizeQuarantine)
	mux.HandleFunc("POST /v1/tasks/{id}/finalize/cancel", h.finalizeCancel)
}

// Routes returns the registered route patterns, used by tests to assert the
// documented surface is present.
func (h *Handler) Routes() []string { return routes }

// health is a liveness probe used by the smoke script and container probes.
func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, Envelope{Data: map[string]string{"status": "ok"}})
}

// --- catalog queries -------------------------------------------------------

func (h *Handler) listNodes(w http.ResponseWriter, r *http.Request) {
	if h.deps.Catalog == nil {
		writeNotImplemented(w)
		return
	}
	writeJSON(w, http.StatusOK, Envelope{Data: h.deps.Catalog.Nodes()})
}

func (h *Handler) listDevices(w http.ResponseWriter, r *http.Request) {
	if h.deps.Catalog == nil {
		writeNotImplemented(w)
		return
	}
	writeJSON(w, http.StatusOK, Envelope{Data: h.deps.Catalog.Devices()})
}

func (h *Handler) listPersonnel(w http.ResponseWriter, r *http.Request) {
	if h.deps.Catalog == nil {
		writeNotImplemented(w)
		return
	}
	writeJSON(w, http.StatusOK, Envelope{Data: h.deps.Catalog.Personnel()})
}

func (h *Handler) listBatches(w http.ResponseWriter, r *http.Request) {
	if h.deps.Catalog == nil {
		writeNotImplemented(w)
		return
	}
	writeJSON(w, http.StatusOK, Envelope{Data: map[string][]catalog.Batch{
		"bolt":   h.deps.Catalog.Batches(catalog.BoltBatch),
		"nut":    h.deps.Catalog.Batches(catalog.NutBatch),
		"washer": h.deps.Catalog.Batches(catalog.WasherBatch),
	}})
}

// --- task lifecycle --------------------------------------------------------

func (h *Handler) lockTask(w http.ResponseWriter, r *http.Request) {
	if h.deps.Tasks == nil {
		writeNotImplemented(w)
		return
	}
	var req task.LockRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	t, err := h.deps.Tasks.Lock(req, h.deps.Catalog)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusCreated, Envelope{Data: t})
}

func (h *Handler) getTask(w http.ResponseWriter, r *http.Request) {
	if h.deps.Tasks == nil || h.deps.Store == nil {
		writeNotImplemented(w)
		return
	}
	id := r.PathValue("id")
	view, err := h.buildView(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, Envelope{Data: view})
}

// buildView assembles the full task projection: state, cursors, active leases,
// sampling closure, sorted reasons and audit summary.
func (h *Handler) buildView(id string) (*TaskView, error) {
	t, err := h.deps.Tasks.Get(id)
	if err != nil {
		return nil, err
	}
	view := &TaskView{
		TaskID:           t.TaskID,
		Generation:       int64(t.Generation),
		Status:           string(t.Status),
		Revision:         int64(t.Revision),
		LockSummary:      t.LockSummary,
		Nodes:            t.Nodes,
		SamplingSet:      t.SamplingSet,
		TorqueBounds:     t.TorqueBounds,
		DeviceCalVersion: t.DeviceCalVersion,
		Credential:       t.Credential,
	}
	// Evidence stream is read through the records ledger.
	if h.deps.Records != nil {
		view.Sampling, err = h.deps.Records.SamplingResults(id)
		if err != nil {
			return nil, err
		}
		view.PairVerifications, err = h.deps.Records.PairVerifications(id)
		if err != nil {
			return nil, err
		}
		view.TorqueRechecks, err = h.deps.Records.TorqueRechecks(id)
		if err != nil {
			return nil, err
		}
		view.Tightenings, err = h.deps.Records.Tightenings(id)
		if err != nil {
			return nil, err
		}
	}
	view.SamplingClosed = len(records.SamplingClosure(view.Sampling, t.SamplingSet)) == 0
	view.Reasons = task.SortedReasons(view.Sampling, t.SamplingSet, t.Nodes)

	err = h.deps.Store.WithTx(context.Background(), func(tx store.Tx) error {
		ctx := context.Background()
		leases, err := tx.ListLeases(ctx, id)
		if err != nil {
			return err
		}
		for _, l := range leases {
			if !l.Released {
				view.ActiveLeases = append(view.ActiveLeases, l)
			}
		}
		reviews, err := tx.ListReviews(ctx, id)
		if err != nil {
			return err
		}
		view.Reviews = reviews
		decision, err := tx.GetDecision(ctx, id)
		if err != nil {
			return err
		}
		view.Decision = decision
		audit, err := tx.ListAudit(ctx, id)
		if err != nil {
			return err
		}
		for _, a := range audit {
			view.AuditKinds = append(view.AuditKinds, a.Kind)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return view, nil
}

// TaskView is the GET projection returned for a task.
type TaskView struct {
	TaskID            string                        `json:"task_id"`
	Generation        int64                         `json:"generation"`
	Status            string                        `json:"status"`
	Revision          int64                         `json:"revision"`
	LockSummary       string                        `json:"lock_summary"`
	Nodes             []domain.TaskNode             `json:"nodes"`
	SamplingSet       []domain.SamplingRef          `json:"sampling_set"`
	TorqueBounds      domain.TorqueBounds           `json:"torque_bounds"`
	DeviceCalVersion  string                        `json:"device_calibration_version"`
	Credential        string                        `json:"credential,omitempty"`
	ActiveLeases      []domain.DeviceLease          `json:"active_leases"`
	PairVerifications []domain.PairVerification     `json:"pair_verifications"`
	TorqueRechecks    []domain.TorqueCoefficientRun `json:"torque_rechecks"`
	Tightenings       []domain.TighteningRecord     `json:"tightenings"`
	Sampling          []domain.SamplingResult       `json:"sampling"`
	SamplingClosed    bool                          `json:"sampling_closed"`
	Reviews           []domain.Review               `json:"reviews"`
	Reasons           []domain.Reason               `json:"reasons"`
	Decision          *domain.FinalDecision         `json:"decision,omitempty"`
	AuditKinds        []string                      `json:"audit_kinds"`
}

// --- JSON helpers ----------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, body Envelope) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, Envelope{Error: newErrorBody(err)})
}

func writeNotImplemented(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotImplemented, Envelope{Error: notImplemented()})
}

// decodeJSON applies the request limits: a 64 KiB body, a bounded number of
// top-level fields, a bounded string length and rejected unknown fields.
func decodeJSON(r *http.Request, dst any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil {
		return errors.New("failed to read request body")
	}
	if len(body) > maxBodyBytes {
		return errors.New("request body exceeds 64 KiB limit")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return errors.New("request body must be a JSON object")
	}
	if len(raw) > maxFields {
		return errors.New("request body has too many fields")
	}
	for _, v := range raw {
		var s string
		if err := json.Unmarshal(v, &s); err == nil && len(s) > maxStringLen {
			return errors.New("string field exceeds maximum length")
		}
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return errors.New("request body contains unknown or malformed fields")
	}
	return nil
}

// routes lists the documented route patterns in registration order.
var routes = []string{
	"GET /",
	"GET /v1/health",
	"GET /v1/catalog/nodes",
	"GET /v1/catalog/batches",
	"GET /v1/catalog/devices",
	"GET /v1/catalog/personnel",
	"POST /v1/tasks",
	"GET /v1/tasks/{id}",
	"POST /v1/tasks/{id}/pair-verifications",
	"POST /v1/tasks/{id}/torque-rechecks",
	"POST /v1/tasks/{id}/tightening/initial",
	"POST /v1/tasks/{id}/tightening/final",
	"POST /v1/tasks/{id}/sampling-results",
	"POST /v1/tasks/{id}/leases/claim",
	"POST /v1/tasks/{id}/leases/release",
	"POST /v1/tasks/{id}/reviews",
	"POST /v1/tasks/{id}/finalize/sign",
	"POST /v1/tasks/{id}/finalize/quarantine",
	"POST /v1/tasks/{id}/finalize/cancel",
}
