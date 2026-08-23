package api

import "boltforge-highstrength-joint-qa/internal/domain"

// ErrorBody is the unified JSON error envelope. Every rejection carries a
// stable code, the aggregate revision and the node/bolt-sorted reason codes.
type ErrorBody struct {
	Code     string   `json:"code"`
	Message  string   `json:"message"`
	Revision int64    `json:"revision,omitempty"`
	Reasons  []string `json:"reasons,omitempty"`
}

// Envelope is the common JSON wrapper for every response body.
type Envelope struct {
	Data  any        `json:"data,omitempty"`
	Error *ErrorBody `json:"error,omitempty"`
}

// newErrorBody converts a domain rejection into the wire envelope.
func newErrorBody(err error) *ErrorBody {
	de, ok := err.(*domain.Error)
	if !ok {
		return &ErrorBody{Code: "INTERNAL", Message: err.Error()}
	}
	body := &ErrorBody{
		Code:     string(de.Code),
		Message:  de.Message,
		Revision: de.Revision,
	}
	if len(de.Reasons) > 0 {
		body.Reasons = de.Codes()
	}
	return body
}

// notImplemented is the stable response returned for a route whose domain
// component has not been wired yet in this foundation build.
func notImplemented() *ErrorBody {
	return &ErrorBody{Code: "NOT_IMPLEMENTED", Message: "route registered but domain component not yet wired"}
}
