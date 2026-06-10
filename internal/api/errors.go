// Package api implements the HTTP/REST surface of the TEO API gateway.
// REST returns RFC 7807 problem+json errors.
package api

import (
	"encoding/json"
	"net/http"

	"github.com/teo-dev/teo/internal/model"
)

// Problem is an RFC 7807 error body.
type Problem struct {
	Type   string       `json:"type"`
	Title  string       `json:"title"`
	Status int          `json:"status"`
	Detail string       `json:"detail,omitempty"`
	Errors []FieldError `json:"errors,omitempty"`
}

// FieldError reports a single validation issue. It is an alias for
// model.FieldError so the shared run-intake service (internal/runsvc) and the
// HTTP layer agree on the wire shape without an import cycle.
type FieldError = model.FieldError

// nolint:unused // used by handlers in the same package
func writeProblem(w http.ResponseWriter, status int, title, detail string, fieldErrs ...FieldError) {
	p := Problem{
		Type:   "about:blank",
		Title:  title,
		Status: status,
		Detail: detail,
		Errors: fieldErrs,
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(p)
}

// nolint:unused // used by handlers in the same package
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
