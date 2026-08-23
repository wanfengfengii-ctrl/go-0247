package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"boltforge-highstrength-joint-qa/internal/catalog"
	"boltforge-highstrength-joint-qa/internal/domain"
)

func TestRoutesCoverDocumentedSurface(t *testing.T) {
	h := NewHandler(Deps{})
	got := h.Routes()
	if len(got) != 19 {
		t.Fatalf("routes = %d, want 19", len(got))
	}
	want := map[string]bool{
		"GET /":                                   true,
		"GET /v1/health":                          true,
		"GET /v1/catalog/nodes":                   true,
		"GET /v1/catalog/batches":                 true,
		"GET /v1/catalog/devices":                 true,
		"GET /v1/catalog/personnel":               true,
		"POST /v1/tasks":                          true,
		"GET /v1/tasks/{id}":                      true,
		"POST /v1/tasks/{id}/reviews":             true,
		"POST /v1/tasks/{id}/finalize/sign":       true,
		"POST /v1/tasks/{id}/finalize/quarantine": true,
		"POST /v1/tasks/{id}/finalize/cancel":     true,
	}
	for _, r := range got {
		if want[r] {
			delete(want, r)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing routes: %v", want)
	}
}

func TestCatalogNodesEndpoint(t *testing.T) {
	h := NewHandler(Deps{Catalog: catalog.Reference()})
	req := httptest.NewRequest(http.MethodGet, "/v1/catalog/nodes", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var env Envelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	nodes, ok := env.Data.([]any)
	if !ok || len(nodes) != 2 {
		t.Fatalf("data = %#v, want 2 nodes", env.Data)
	}
}

func TestNotImplementedWhenComponentUnwired(t *testing.T) {
	h := NewHandler(Deps{})
	req := httptest.NewRequest(http.MethodGet, "/v1/tasks/abc", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rr.Code)
	}
	var env Envelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error == nil || env.Error.Code != "NOT_IMPLEMENTED" {
		t.Fatalf("error = %+v, want NOT_IMPLEMENTED", env.Error)
	}
}

func TestEnvelopeCarriesErrorCodeAndReasons(t *testing.T) {
	err := domain.NewError(domain.CodeSequenceGap, "gap").
		WithReasons([]domain.Reason{
			{Code: domain.CodeSequenceGap, Node: "N-02", Bolt: 1},
			{Code: domain.CodeSequenceGap, Node: "N-01", Bolt: 2},
		})
	body := newErrorBody(err)
	if body.Code != "SEQUENCE_GAP" {
		t.Fatalf("code = %q", body.Code)
	}
	if len(body.Reasons) != 2 || body.Reasons[0] != "SEQUENCE_GAP" {
		t.Fatalf("reasons = %v", body.Reasons)
	}
}

func TestDecodeJSONRejectsUnknownFields(t *testing.T) {
	type payload struct {
		TaskID string `json:"task_id"`
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/tasks", strings.NewReader(`{"task_id":"t","bogus":1}`))
	var p payload
	if err := decodeJSON(req, &p); err == nil {
		t.Fatal("expected unknown field to be rejected")
	}
}

func TestDecodeJSONRejectsOversizedString(t *testing.T) {
	type payload struct {
		TaskID string `json:"task_id"`
	}
	long := strings.Repeat("x", maxStringLen+1)
	req := httptest.NewRequest(http.MethodPost, "/v1/tasks", strings.NewReader(`{"task_id":"`+long+`"}`))
	var p payload
	if err := decodeJSON(req, &p); err == nil {
		t.Fatal("expected oversized string to be rejected")
	}
}

func TestDecodeJSONAcceptsValidBody(t *testing.T) {
	type payload struct {
		TaskID string `json:"task_id"`
		Gen    int64  `json:"generation"`
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/tasks", strings.NewReader(`{"task_id":"t-1","generation":2}`))
	var p payload
	if err := decodeJSON(req, &p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.TaskID != "t-1" || p.Gen != 2 {
		t.Fatalf("payload = %+v", p)
	}
}
