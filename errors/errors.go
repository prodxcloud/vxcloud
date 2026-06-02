// Package errors is the typed error hierarchy returned by the SDK.
//
// All SDK methods return errors that satisfy the standard error interface.
// To branch on category, use errors.As:
//
//	var authErr *vxerrors.AuthError
//	if errors.As(err, &authErr) {
//	    // re-login flow
//	}
package vxerrors

import (
	"errors"
	"fmt"
)

// Failure is the generic error type. Every concrete typed error in this
// package embeds *Failure, so its Op / HTTPStatus / Cause fields are
// always accessible.
//
// The type is named Failure (not Error) deliberately — embedding a struct
// named Error would shadow the Error() method on the outer wrapper types.
type Failure struct {
	// Op is the SDK operation that failed (e.g. "cicd.Pipelines.List").
	Op string
	// HTTPStatus is the upstream HTTP status if the failure was an API call.
	HTTPStatus int
	// Message is a short, human-readable description.
	Message string
	// Detail is the unwrapped upstream error body when present.
	Detail string
	// Cause is the underlying Go error if any.
	Cause error
}

func (e *Failure) Error() string {
	switch {
	case e.HTTPStatus != 0 && e.Detail != "":
		return fmt.Sprintf("%s: %d %s — %s", e.Op, e.HTTPStatus, e.Message, e.Detail)
	case e.HTTPStatus != 0:
		return fmt.Sprintf("%s: %d %s", e.Op, e.HTTPStatus, e.Message)
	case e.Cause != nil:
		return fmt.Sprintf("%s: %s: %v", e.Op, e.Message, e.Cause)
	default:
		return fmt.Sprintf("%s: %s", e.Op, e.Message)
	}
}

func (e *Failure) Unwrap() error { return e.Cause }

// AuthError indicates the credential was rejected (401/403) or invalid in
// shape. The caller's response is to obtain a new credential — retrying
// with the same key will not succeed.
type AuthError struct{ *Failure }

// ValidationError indicates the request payload was malformed (400 / 422).
// The caller must fix the payload — retrying as-is will fail.
type ValidationError struct {
	*Failure
	// Fields names the offending field(s) when the server returned them.
	Fields []string
}

// RateLimitError indicates the caller exceeded a quota (429). RetryAfter is
// the suggested wait, derived from the Retry-After header if present.
type RateLimitError struct {
	*Failure
	RetryAfter int // seconds; 0 if not advertised
}

// ServerError indicates an upstream 5xx. Safe to retry with backoff.
type ServerError struct{ *Failure }

// NetworkError indicates the request did not reach the server (DNS, TCP,
// TLS, timeout). Safe to retry with backoff.
type NetworkError struct{ *Failure }

// NotFoundError indicates the resource does not exist (404).
type NotFoundError struct{ *Failure }

// IsRetryable reports whether retrying the same request, possibly after
// backing off, is appropriate.
func IsRetryable(err error) bool {
	var ne *NetworkError
	var se *ServerError
	var rl *RateLimitError
	switch {
	case errors.As(err, &ne):
		return true
	case errors.As(err, &se):
		return true
	case errors.As(err, &rl):
		return true
	}
	return false
}

// FromHTTP constructs the appropriate concrete error type for an HTTP
// response. Used by the transport layer; callers should not need this
// directly.
func FromHTTP(op string, status int, message, detail string) error {
	base := &Failure{Op: op, HTTPStatus: status, Message: message, Detail: detail}
	switch {
	case status == 401 || status == 403:
		return &AuthError{Failure: base}
	case status == 400 || status == 422:
		return &ValidationError{Failure: base}
	case status == 404:
		return &NotFoundError{Failure: base}
	case status == 429:
		return &RateLimitError{Failure: base}
	case status >= 500:
		return &ServerError{Failure: base}
	default:
		return base
	}
}
