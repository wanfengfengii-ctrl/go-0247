package api

import (
	"net/http"

	"boltforge-highstrength-joint-qa/internal/arbiter"
	"boltforge-highstrength-joint-qa/internal/domain"
	"boltforge-highstrength-joint-qa/internal/ledger"
	"boltforge-highstrength-joint-qa/internal/task"
)

// pairVerification submits a connection-pair identity and socket.
func (h *Handler) pairVerification(w http.ResponseWriter, r *http.Request) {
	if h.deps.Tasks == nil {
		writeNotImplemented(w)
		return
	}
	var req task.PairVerifyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := h.deps.Tasks.VerifyPair(r.PathValue("id"), req.Revision, req); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	h.writeTaskView(w, r.PathValue("id"), http.StatusOK)
}

// torqueRecheck submits one torque-coefficient recheck trial.
func (h *Handler) torqueRecheck(w http.ResponseWriter, r *http.Request) {
	if h.deps.Tasks == nil {
		writeNotImplemented(w)
		return
	}
	var req task.RecheckRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := h.deps.Tasks.RecheckTorque(r.PathValue("id"), req.Revision, req); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	h.writeTaskView(w, r.PathValue("id"), http.StatusOK)
}

func (h *Handler) initialTightening(w http.ResponseWriter, r *http.Request) {
	if h.deps.Tasks == nil {
		writeNotImplemented(w)
		return
	}
	var req task.TightenRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := h.deps.Tasks.TightenInitial(r.PathValue("id"), req.Revision, req); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	h.writeTaskView(w, r.PathValue("id"), http.StatusOK)
}

func (h *Handler) finalTightening(w http.ResponseWriter, r *http.Request) {
	if h.deps.Tasks == nil {
		writeNotImplemented(w)
		return
	}
	var req task.TightenRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := h.deps.Tasks.TightenFinal(r.PathValue("id"), req.Revision, req); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	h.writeTaskView(w, r.PathValue("id"), http.StatusOK)
}

func (h *Handler) samplingResult(w http.ResponseWriter, r *http.Request) {
	if h.deps.Tasks == nil {
		writeNotImplemented(w)
		return
	}
	var req task.SamplingRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := h.deps.Tasks.SubmitSampling(r.PathValue("id"), req.Revision, req); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	h.writeTaskView(w, r.PathValue("id"), http.StatusOK)
}

// claimLease atomically claims a connection token (token_id) or a device lease
// (device_id) for the task.
func (h *Handler) claimLease(w http.ResponseWriter, r *http.Request) {
	if h.deps.Ledger == nil || h.deps.Catalog == nil {
		writeNotImplemented(w)
		return
	}
	var body struct {
		TokenID    string             `json:"token_id"`
		DeviceID   string             `json:"device_id"`
		Generation domain.Generation  `json:"generation"`
		HoldSec    int64              `json:"hold_seconds"`
		NodeID     string             `json:"node_id"`
		BoltNo     int                `json:"bolt_no"`
		Operation  domain.OperationNo `json:"operation_no"`
		OperatorID string             `json:"operator_id"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	id := r.PathValue("id")
	if body.TokenID != "" {
		tok, err := h.deps.Ledger.ClaimToken(ledger.ClaimTokenRequest{
			TaskID: id, OperationNo: body.Operation, OperatorID: body.OperatorID,
			TokenID: body.TokenID, NodeID: body.NodeID, BoltNo: body.BoltNo,
		})
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusCreated, Envelope{Data: tok})
		return
	}
	dev, ok := h.deps.Catalog.DeviceByID(body.DeviceID)
	if !ok {
		writeError(w, http.StatusBadRequest, domain.NewError(domain.CodeBatchMismatch, "device not found"))
		return
	}
	l, err := h.deps.Ledger.ClaimLease(ledger.ClaimLeaseRequest{
		TaskID: id, OperationNo: body.Operation, OperatorID: body.OperatorID,
		DeviceID: body.DeviceID, Generation: body.Generation,
		CalibrationVersion: dev.CalibrationVersion, HoldSeconds: body.HoldSec,
	})
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusCreated, Envelope{Data: l})
}

// releaseLease releases a connection token or a device lease.
func (h *Handler) releaseLease(w http.ResponseWriter, r *http.Request) {
	if h.deps.Ledger == nil {
		writeNotImplemented(w)
		return
	}
	var req struct {
		TokenID   string             `json:"token_id"`
		LeaseID   string             `json:"lease_id"`
		Operation domain.OperationNo `json:"operation_no"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	id := r.PathValue("id")
	var err error
	if req.TokenID != "" {
		err = h.deps.Ledger.ReleaseToken(id, req.TokenID, req.Operation)
	} else if req.LeaseID != "" {
		err = h.deps.Ledger.ReleaseLease(id, req.LeaseID, req.Operation)
	} else {
		writeError(w, http.StatusBadRequest, domain.NewError(domain.CodeNodeMismatch, "token_id or lease_id required"))
		return
	}
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, Envelope{Data: map[string]string{"released": "ok"}})
}

// submitReview submits one independent review seat.
func (h *Handler) submitReview(w http.ResponseWriter, r *http.Request) {
	if h.deps.Arbiter == nil || h.deps.Catalog == nil {
		writeNotImplemented(w)
		return
	}
	var req arbiter.ReviewRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	person, ok := h.deps.Catalog.PersonnelByID(req.PersonID)
	if !ok {
		writeError(w, http.StatusBadRequest, domain.NewError(domain.CodeReviewerNotQualified, "reviewer not in directory"))
		return
	}
	if err := h.deps.Arbiter.SubmitReview(req, person.QualificationVersion, h.deps.Catalog.CurrentQualificationVersion()); err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	h.writeTaskView(w, r.PathValue("id"), http.StatusOK)
}

func (h *Handler) finalizeSign(w http.ResponseWriter, r *http.Request) {
	h.finalize(w, r, arbiter.FinalSign)
}
func (h *Handler) finalizeQuarantine(w http.ResponseWriter, r *http.Request) {
	h.finalize(w, r, arbiter.FinalQuarantine)
}
func (h *Handler) finalizeCancel(w http.ResponseWriter, r *http.Request) {
	h.finalize(w, r, arbiter.FinalCancel)
}

func (h *Handler) finalize(w http.ResponseWriter, r *http.Request, ft arbiter.FinalType) {
	if h.deps.Arbiter == nil {
		writeNotImplemented(w)
		return
	}
	var req arbiter.FinalizeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	req.Type = ft
	decision, err := h.deps.Arbiter.Finalize(req)
	if err != nil {
		writeError(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, Envelope{Data: decision})
}

func (h *Handler) writeTaskView(w http.ResponseWriter, id string, status int) {
	view, err := h.buildView(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, status, Envelope{Data: view})
}
