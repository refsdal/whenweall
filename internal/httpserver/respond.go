package httpserver

// Shared JSON envelope helpers, added in Task 7 for internal/polls's HTTP handlers (and reusable
// by any later plan's handlers) so every /api/v1/* JSON response — success or error — is built the
// same way in one place, rather than each handler hand-rolling its own json.NewEncoder call the
// way writeErrorEnvelope's several near-duplicates (this package's own, and internal/auth's) had
// already started to. The existing writeErrorEnvelope call sites in this package are left as-is
// (out of this task's scope to touch); JSON/Err are additive.

import (
	"encoding/json"
	"net/http"
)

// JSON writes body as a JSON response with the given status code and Content-Type.
func JSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// ErrBody is the standard error envelope's inner "error" object.
type ErrBody struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

// Err writes the standard {"error":{"code","message","fields"}} envelope. fields is nil for every
// error except a validation failure (code "invalid"), whose Fields map (field path -> message) the
// client needs to point at the offending inputs.
func Err(w http.ResponseWriter, status int, code, message string, fields map[string]string) {
	JSON(w, status, map[string]any{"error": ErrBody{Code: code, Message: message, Fields: fields}})
}
