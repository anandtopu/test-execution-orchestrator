// Package api implements the HTTP/REST surface of the TEO API gateway.
// REST returns RFC 7807 problem+json errors.
package api

import (
	"encoding/json"
	"net/http"
)

// Problem is an RFC 7807 error body.
type Problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
	Errors []FieldError `json:"errors,omitempty"`
}

// FieldError reports a single validation issue.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

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
