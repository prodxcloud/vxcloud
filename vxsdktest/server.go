// Package vxsdktest provides a stub HTTP server for unit-testing code
// that uses vxsdk-go without a live tenant or control plane.
//
// Typical usage:
//
//	srv := vxsdktest.NewServer()
//	defer srv.Close()
//	srv.Handle("POST", "/api/v1/auth/developer/keys/login", vxsdktest.JSON(200, map[string]any{
//	    "access":  "test-jwt",
//	    "refresh": "test-refresh",
//	    "user":    map[string]any{"username": "alice"},
//	}))
//	srv.Handle("GET", "/api/v2/cicd/pipelines", vxsdktest.JSON(200, map[string]any{
//	    "data":  []any{ map[string]any{"id":"p1","name":"x","provider":"github","repository_url":"https://x"} },
//	    "count": 1,
//	}))
//
//	c, _ := vxsdk.New(ctx,
//	    vxsdk.WithAPIKey("xc_dev_xxxxxxxxxxxxxxxx"),
//	    vxsdk.WithUsername("alice"),
//	    vxsdk.WithInfinityURL(srv.URL()),
//	    vxsdk.WithNodeURL(srv.URL()),
//	)
//	pipelines, err := c.CICD().Pipelines().List(ctx)
//	// assert pipelines / err …
package vxsdktest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
)

// Server is an httptest.Server wrapped with a lookup table of registered
// handlers keyed by (method, path). Unmatched requests return 404.
type Server struct {
	mu       sync.Mutex
	srv      *httptest.Server
	handlers map[string]http.HandlerFunc

	// Calls records every (method, path) the server received, in order.
	// Useful for asserting "did the SDK make the requests we expected?".
	Calls []Call
}

// Call records one received request.
type Call struct {
	Method  string
	Path    string
	Headers http.Header
	Body    []byte
}

// NewServer constructs and starts a new test server. Caller must Close().
func NewServer() *Server {
	s := &Server{handlers: map[string]http.HandlerFunc{}}
	s.srv = httptest.NewServer(http.HandlerFunc(s.dispatch))
	return s
}

// URL returns the base URL of the running server.
func (s *Server) URL() string { return s.srv.URL }

// Close stops the server.
func (s *Server) Close() { s.srv.Close() }

// Handle registers a fixed handler for the given (method, path).
func (s *Server) Handle(method, path string, h http.HandlerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method+" "+path] = h
}

func (s *Server) dispatch(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.Calls = append(s.Calls, Call{Method: r.Method, Path: r.URL.Path, Headers: r.Header.Clone(), Body: body})
	h, ok := s.handlers[r.Method+" "+r.URL.Path]
	s.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"vxsdktest: no handler for ` + r.Method + " " + r.URL.Path + `"}`))
		return
	}
	// Restore body for the inner handler.
	r.Body = io.NopCloser(byteReader(body))
	h(w, r)
}

// JSON returns a handler that writes a JSON body with the given status.
func JSON(status int, body any) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
}

// Status returns a handler that writes an empty body with the given status.
// Useful for 204s and 401s.
func Status(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }
}

// Sequence returns a handler that returns the given handlers in order,
// one per call. After exhaustion it 500s. Useful for testing retry +
// auto-refresh paths (first call 401, second call 200).
func Sequence(steps ...http.HandlerFunc) http.HandlerFunc {
	var i int
	var mu sync.Mutex
	return func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		idx := i
		if idx < len(steps) {
			i++
		}
		mu.Unlock()
		if idx >= len(steps) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"detail":"vxsdktest.Sequence exhausted"}`))
			return
		}
		steps[idx](w, r)
	}
}

// ── tiny io.Reader over []byte without pulling in bytes.NewReader ─

type byteReader []byte

func (b byteReader) Read(p []byte) (int, error) {
	if len(b) == 0 {
		return 0, io.EOF
	}
	n := copy(p, b)
	return n, nil
}
