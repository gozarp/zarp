// Copyright 2026 Subhanjan Adhikary. All rights reserved.
// Use of this source code is governed by a MIT style
// license that can be found in the LICENSE file.

package middleware_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gozarp/zarp"
	"github.com/gozarp/zarp/middleware"
)

func serve(e *zarp.Engine, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

// ---------------------------------------------------------------- Recovery

func TestRecoveryTurnsPanicInto500(t *testing.T) {
	var log bytes.Buffer
	e := zarp.New()
	e.Use(middleware.RecoveryWithWriter(&log))
	e.GET("/boom", func(c *zarp.Context) { panic("handler exploded") })

	rec := serve(e, "GET", "/boom")

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("code = %d, want 500", rec.Code)
	}
	if !strings.Contains(log.String(), "handler exploded") {
		t.Errorf("log = %q, want the panic value", log.String())
	}
	if !strings.Contains(log.String(), "/boom") {
		t.Errorf("log = %q, want the request path", log.String())
	}
}

func TestRecoveryStopsTheChain(t *testing.T) {
	var ran []string
	e := zarp.New()
	e.Use(middleware.RecoveryWithWriter(nil))
	e.GET("/boom",
		func(c *zarp.Context) { ran = append(ran, "first"); c.Next() },
		func(c *zarp.Context) { panic("x") },
		func(c *zarp.Context) { ran = append(ran, "after-panic") },
	)

	serve(e, "GET", "/boom")

	if got := strings.Join(ran, " "); got != "first" {
		t.Errorf("ran = %q, want nothing after the panic", got)
	}
}

func TestRecoveryKeepsServerUsable(t *testing.T) {
	// A recovered panic unwinds inside Next, so the Context still returns to
	// the pool and later requests are unaffected.
	e := zarp.New()
	e.Use(middleware.RecoveryWithWriter(nil))
	e.GET("/boom", func(c *zarp.Context) { panic("x") })
	e.GET("/ok/:id", func(c *zarp.Context) { c.Text(http.StatusOK, c.Param("id")) })

	for range 5 {
		serve(e, "GET", "/boom")
		if got := serve(e, "GET", "/ok/42").Body.String(); got != "42" {
			t.Fatalf("after a panic, /ok/42 = %q", got)
		}
	}
}

func TestRecoveryRepanicsErrAbortHandler(t *testing.T) {
	e := zarp.New()
	e.Use(middleware.RecoveryWithWriter(nil))
	e.GET("/abort", func(c *zarp.Context) { panic(http.ErrAbortHandler) })

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("ErrAbortHandler was swallowed; net/http uses it as a signal")
		}
		if err, ok := r.(error); !ok || !errors.Is(err, http.ErrAbortHandler) {
			t.Errorf("re-panicked with %v, want ErrAbortHandler", r)
		}
	}()

	serve(e, "GET", "/abort")
}

func TestRecoveryIgnoresBrokenPipe(t *testing.T) {
	var log bytes.Buffer
	e := zarp.New()
	e.Use(middleware.RecoveryWithWriter(&log))
	e.GET("/gone", func(c *zarp.Context) {
		panic(&net.OpError{
			Op:  "write",
			Net: "tcp",
			Err: &os.SyscallError{Syscall: "write", Err: errors.New("broken pipe")},
		})
	})

	rec := serve(e, "GET", "/gone")

	if log.Len() != 0 {
		t.Errorf("log = %q, want silence — the client is gone, not a crash", log.String())
	}
	if rec.Code == http.StatusInternalServerError {
		t.Error("wrote 500 to a dead connection")
	}
}

func TestRecoveryWithHandler(t *testing.T) {
	e := zarp.New()
	e.Use(middleware.RecoveryWithHandler(func(c *zarp.Context, err any) {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable,
			map[string]string{"error": "try later"})
	}))
	e.GET("/boom", func(c *zarp.Context) { panic("x") })

	rec := serve(e, "GET", "/boom")

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("code = %d, want 503", rec.Code)
	}
	if got := rec.Body.String(); got != `{"error":"try later"}` {
		t.Errorf("body = %q", got)
	}
}

func TestRecoveryPassesThroughNormalRequests(t *testing.T) {
	e := zarp.New()
	e.Use(middleware.RecoveryWithWriter(nil))
	e.GET("/ok", func(c *zarp.Context) { c.Text(http.StatusOK, "fine") })

	rec := serve(e, "GET", "/ok")
	if rec.Code != http.StatusOK || rec.Body.String() != "fine" {
		t.Errorf("code=%d body=%q", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------- Logger

func TestLoggerWritesRequestDetails(t *testing.T) {
	var log bytes.Buffer
	e := zarp.New()
	e.Use(middleware.LoggerWithWriter(&log))
	e.GET("/user/:id", func(c *zarp.Context) { c.Text(http.StatusCreated, "hi") })

	serve(e, "GET", "/user/42")

	line := log.String()
	for _, want := range []string{"201", "GET", "/user/:id"} {
		if !strings.Contains(line, want) {
			t.Errorf("log = %q, want it to contain %q", line, want)
		}
	}
	if strings.Contains(line, "/user/42") {
		t.Errorf("log = %q — it should carry the route pattern, not the id", line)
	}
	if !strings.HasSuffix(line, "\n") {
		t.Error("log line is not newline-terminated")
	}
}

func TestLoggerLogsAfterTheChain(t *testing.T) {
	// The status is only known once everything downstream has run.
	var log bytes.Buffer
	e := zarp.New()
	e.Use(middleware.LoggerWithWriter(&log))
	e.Use(func(c *zarp.Context) {
		c.Next()
		c.Status(http.StatusTeapot) // changed after the handler
	})
	e.GET("/x", func(c *zarp.Context) { c.Status(http.StatusOK) })

	serve(e, "GET", "/x")

	if !strings.Contains(log.String(), "418") {
		t.Errorf("log = %q, want the final status", log.String())
	}
}

func TestLoggerSkipPaths(t *testing.T) {
	var log bytes.Buffer
	e := zarp.New()
	e.Use(middleware.LoggerWithConfig(middleware.LoggerConfig{
		Sink:      middleware.TextSink(&log),
		SkipPaths: []string{"/healthz"},
	}))
	e.GET("/healthz", func(c *zarp.Context) { c.Text(http.StatusOK, "ok") })
	e.GET("/real", func(c *zarp.Context) { c.Text(http.StatusOK, "ok") })

	if rec := serve(e, "GET", "/healthz"); rec.Body.String() != "ok" {
		t.Errorf("skipped path must still be served, got %q", rec.Body)
	}
	if log.Len() != 0 {
		t.Errorf("log = %q, want nothing for a skipped path", log.String())
	}

	serve(e, "GET", "/real")
	if log.Len() == 0 {
		t.Error("skipping one path silenced the others")
	}
}

func TestLoggerCustomSink(t *testing.T) {
	var log bytes.Buffer
	e := zarp.New()
	e.Use(middleware.LoggerWith(middleware.SinkFunc(func(e middleware.Entry) {
		fmt.Fprintf(&log, "%s %s %s\n", e.Method, e.Path, e.URL)
	})))
	e.GET("/user/:id", func(c *zarp.Context) {})

	serve(e, "GET", "/user/42?q=go")

	if got := log.String(); got != "GET /user/:id /user/42?q=go\n" {
		t.Errorf("log = %q", got)
	}
}

func TestLoggerEntryFields(t *testing.T) {
	var got middleware.Entry
	e := zarp.New()
	e.Use(middleware.LoggerWith(middleware.SinkFunc(func(e middleware.Entry) { got = e })))
	e.GET("/user/:id", func(c *zarp.Context) { c.Text(http.StatusOK, "hello") })

	serve(e, "GET", "/user/42?q=go")

	if got.Status != http.StatusOK {
		t.Errorf("Status = %d", got.Status)
	}
	if got.Size != 5 {
		t.Errorf("Size = %d, want 5", got.Size)
	}
	if got.Path != "/user/:id" || got.URL != "/user/42?q=go" {
		t.Errorf("Path = %q URL = %q", got.Path, got.URL)
	}
	// A trivial handler can round to exactly 0 where the monotonic clock is
	// coarse, so only a negative latency is a bug.
	if got.Latency < 0 {
		t.Errorf("Latency = %v", got.Latency)
	}
	if got.ClientIP == "" {
		t.Error("ClientIP is empty")
	}
}

func TestLoggerLogsMisses(t *testing.T) {
	var log bytes.Buffer
	e := zarp.New()
	e.Use(middleware.LoggerWithWriter(&log))
	e.GET("/known", func(c *zarp.Context) {})

	serve(e, "GET", "/unknown")

	line := log.String()
	if !strings.Contains(line, "404") || !strings.Contains(line, "/unknown") {
		t.Errorf("log = %q, want the 404 and the URL it was for", line)
	}
}

// ---------------------------------------------------------------- Default

func TestDefaultBundlesLoggerThenRecovery(t *testing.T) {
	got := middleware.Default()
	if len(got) != 2 {
		t.Fatalf("Default() has %d handlers, want 2", len(got))
	}

	// Order matters: the logger has to wrap recovery to record the 500.
	var log bytes.Buffer
	e := zarp.New()
	e.Use(middleware.LoggerWithWriter(&log), middleware.RecoveryWithWriter(nil))
	e.GET("/boom", func(c *zarp.Context) { panic("x") })

	rec := serve(e, "GET", "/boom")

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("code = %d", rec.Code)
	}
	if !strings.Contains(log.String(), "500") {
		t.Errorf("log = %q, want the recovered 500 recorded", log.String())
	}
}

// ---------------------------------------------------------------- benchmarks

func BenchmarkLogger(b *testing.B) {
	e := zarp.New()
	e.Use(middleware.LoggerWithWriter(io.Discard))
	e.GET("/user/:id", func(c *zarp.Context) { c.Text(http.StatusOK, "ok") })
	benchServe(b, e, "GET", "/user/42")
}

func BenchmarkRecovery(b *testing.B) {
	e := zarp.New()
	e.Use(middleware.RecoveryWithWriter(nil))
	e.GET("/user/:id", func(c *zarp.Context) { c.Text(http.StatusOK, "ok") })
	benchServe(b, e, "GET", "/user/42")
}

func BenchmarkNoMiddleware(b *testing.B) {
	e := zarp.New()
	e.GET("/user/:id", func(c *zarp.Context) { c.Text(http.StatusOK, "ok") })
	benchServe(b, e, "GET", "/user/42")
}

type nopWriter struct{ header http.Header }

func (w *nopWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}
func (w *nopWriter) Write(b []byte) (int, error)       { return len(b), nil }
func (w *nopWriter) WriteString(s string) (int, error) { return len(s), nil }
func (w *nopWriter) WriteHeader(int)                   {}

func benchServe(b *testing.B, h http.Handler, method, target string) {
	req := httptest.NewRequest(method, target, nil)
	w := &nopWriter{}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		h.ServeHTTP(w, req)
	}
}

// ---------------------------------------------------------------- RequestID

func TestRequestIDGenerates(t *testing.T) {
	var seen string
	e := zarp.New()
	e.Use(middleware.RequestID())
	e.GET("/x", func(c *zarp.Context) { seen = middleware.GetRequestID(c) })

	rec := serve(e, "GET", "/x")

	if len(seen) != 32 {
		t.Errorf("id = %q, want 32 hex chars", seen)
	}
	if got := rec.Header().Get(middleware.RequestIDHeader); got != seen {
		t.Errorf("header = %q, want it echoed as %q", got, seen)
	}
}

func TestRequestIDIsUniquePerRequest(t *testing.T) {
	e := zarp.New()
	e.Use(middleware.RequestID())
	e.GET("/x", func(c *zarp.Context) {})

	seen := make(map[string]bool, 100)
	for range 100 {
		id := serve(e, "GET", "/x").Header().Get(middleware.RequestIDHeader)
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}

func TestRequestIDReusesInbound(t *testing.T) {
	var seen string
	e := zarp.New()
	e.Use(middleware.RequestIDWithConfig(middleware.RequestIDConfig{TrustInbound: true}))
	e.GET("/x", func(c *zarp.Context) { seen = middleware.GetRequestID(c) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set(middleware.RequestIDHeader, "upstream-trace-123")
	e.ServeHTTP(rec, req)

	if seen != "upstream-trace-123" {
		t.Errorf("id = %q, want the inbound value so a trace spans services", seen)
	}
}

func TestRequestIDRejectsJunkInbound(t *testing.T) {
	cases := map[string]string{
		"control chars": "abc\r\nX-Injected: evil",
		"too long":      strings.Repeat("a", 200),
		"empty":         "",
	}
	for name, bad := range cases {
		t.Run(name, func(t *testing.T) {
			var seen string
			e := zarp.New()
			e.Use(middleware.RequestIDWithConfig(middleware.RequestIDConfig{TrustInbound: true}))
			e.GET("/x", func(c *zarp.Context) { seen = middleware.GetRequestID(c) })

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			req.Header[middleware.RequestIDHeader] = []string{bad}
			e.ServeHTTP(rec, req)

			if seen == bad {
				t.Errorf("kept junk id %q", bad)
			}
			if len(seen) != 32 {
				t.Errorf("id = %q, want a freshly generated one", seen)
			}
		})
	}
}

func TestRequestIDUntrustedInbound(t *testing.T) {
	var seen string
	e := zarp.New()
	// The zero config is the untrusting one: an inbound id is client input.
	e.Use(middleware.RequestID())
	e.GET("/x", func(c *zarp.Context) { seen = middleware.GetRequestID(c) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set(middleware.RequestIDHeader, "from-client")
	e.ServeHTTP(rec, req)

	if seen == "from-client" {
		t.Error("inbound id used by default: TrustInbound must be opt-in")
	}
	if len(seen) != 32 {
		t.Errorf("id = %q, want a freshly generated one", seen)
	}
}

func TestRequestIDAcceptsCommonTraceFormats(t *testing.T) {
	ids := []string{
		"0af7651916cd43dd8448eb211c80319c",     // W3C trace id
		"550e8400-e29b-41d4-a716-446655440000", // UUID
		"01ARZ3NDEKTSV4RRFFQ69G5FAV",           // ULID
		"projects_1:trace.7",                   // punctuation we allow
	}
	for _, id := range ids {
		t.Run(id, func(t *testing.T) {
			var seen string
			e := zarp.New()
			e.Use(middleware.RequestIDWithConfig(middleware.RequestIDConfig{TrustInbound: true}))
			e.GET("/x", func(c *zarp.Context) { seen = middleware.GetRequestID(c) })

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			req.Header.Set(middleware.RequestIDHeader, id)
			e.ServeHTTP(rec, req)

			if seen != id {
				t.Errorf("id = %q, want the inbound %q", seen, id)
			}
		})
	}
}

func TestCORSRejectsMalformedOrigins(t *testing.T) {
	bad := map[string]string{
		"trailing slash": "https://example.com/",
		"no scheme":      "example.com",
		"path":           "https://example.com/app",
		"query":          "https://example.com?a=1",
		"null":           "null",
		"userinfo":       "https://user@example.com",
		"uppercase host": "https://Example.com",
	}
	for name, origin := range bad {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("origin %q was accepted", origin)
				}
			}()
			middleware.CORSWithConfig(middleware.CORSConfig{AllowOrigins: []string{origin}})
		})
	}
}

func TestCORSAcceptsWellFormedOrigins(t *testing.T) {
	ok := []string{"https://example.com", "http://localhost:3000", "https://a.b.example.com:8443"}
	for _, origin := range ok {
		t.Run(origin, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("origin %q was rejected: %v", origin, r)
				}
			}()
			middleware.CORSWithConfig(middleware.CORSConfig{AllowOrigins: []string{origin}})
		})
	}
}

func TestRequestIDCustomHeaderAndGenerator(t *testing.T) {
	e := zarp.New()
	e.Use(middleware.RequestIDWithConfig(middleware.RequestIDConfig{
		Header:    "X-Trace",
		Generator: func() string { return "fixed" },
	}))
	e.GET("/x", func(c *zarp.Context) {})

	rec := serve(e, "GET", "/x")
	if got := rec.Header().Get("X-Trace"); got != "fixed" {
		t.Errorf("X-Trace = %q", got)
	}
}

// ---------------------------------------------------------------- CORS

func corsRequest(e *zarp.Engine, method, origin, preflightFor string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, "/x", nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if preflightFor != "" {
		req.Header.Set("Access-Control-Request-Method", preflightFor)
	}
	e.ServeHTTP(rec, req)
	return rec
}

func corsEngine(cfg middleware.CORSConfig) *zarp.Engine {
	e := zarp.New()
	e.Use(middleware.CORSWithConfig(cfg))
	e.GET("/x", func(c *zarp.Context) { c.Text(http.StatusOK, "ok") })
	e.OPTIONS("/x", func(c *zarp.Context) { c.Text(http.StatusOK, "route-options") })
	return e
}

func TestCORSAllowsAnyOriginByDefault(t *testing.T) {
	e := zarp.New()
	e.Use(middleware.CORS())
	e.GET("/x", func(c *zarp.Context) { c.Text(http.StatusOK, "ok") })

	rec := corsRequest(e, "GET", "https://example.com", "")

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("allow-origin = %q", got)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("handler did not run: %q", rec.Body)
	}
	if !strings.Contains(rec.Header().Get("Vary"), "Origin") {
		t.Error("Vary: Origin missing — a shared cache would cross origins")
	}
}

func TestCORSNoOriginIsUntouched(t *testing.T) {
	e := corsEngine(middleware.CORSConfig{AllowOrigins: []string{"https://ok.example"}})

	rec := corsRequest(e, "GET", "", "")
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("same-origin request got CORS headers")
	}
	if rec.Body.String() != "ok" {
		t.Errorf("handler did not run: %q", rec.Body)
	}
}

func TestCORSRejectsUnlistedOrigin(t *testing.T) {
	e := corsEngine(middleware.CORSConfig{AllowOrigins: []string{"https://ok.example"}})

	rec := corsRequest(e, "GET", "https://evil.example", "")

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("allow-origin = %q, want none for an unlisted origin", got)
	}
	// The request still runs: CORS restricts what a browser lets a script
	// read, it is not an authorization mechanism.
	if rec.Body.String() != "ok" {
		t.Errorf("body = %q", rec.Body)
	}
}

func TestCORSOriginFunc(t *testing.T) {
	e := corsEngine(middleware.CORSConfig{
		AllowOriginFunc: func(origin string) bool {
			return strings.HasSuffix(origin, ".trusted.example")
		},
	})

	if got := corsRequest(e, "GET", "https://a.trusted.example", "").
		Header().Get("Access-Control-Allow-Origin"); got != "https://a.trusted.example" {
		t.Errorf("allow-origin = %q", got)
	}
	if got := corsRequest(e, "GET", "https://nope.example", "").
		Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("allow-origin = %q, want none", got)
	}
}

func TestCORSPreflight(t *testing.T) {
	e := corsEngine(middleware.CORSConfig{
		AllowOrigins:  []string{"https://ok.example"},
		AllowHeaders:  []string{"Content-Type", "X-Custom"},
		ExposeHeaders: []string{"X-Total-Count"},
		MaxAge:        time.Hour,
	})

	rec := corsRequest(e, "OPTIONS", "https://ok.example", "POST")

	if rec.Code != http.StatusNoContent {
		t.Errorf("code = %d, want 204", rec.Code)
	}
	if rec.Body.String() == "route-options" {
		t.Error("preflight reached the route handler; it should be aborted")
	}
	h := rec.Header()
	if !strings.Contains(h.Get("Access-Control-Allow-Headers"), "X-Custom") {
		t.Errorf("allow-headers = %q", h.Get("Access-Control-Allow-Headers"))
	}
	if !strings.Contains(h.Get("Access-Control-Allow-Methods"), "POST") {
		t.Errorf("allow-methods = %q", h.Get("Access-Control-Allow-Methods"))
	}
	if h.Get("Access-Control-Max-Age") != "3600" {
		t.Errorf("max-age = %q, want seconds", h.Get("Access-Control-Max-Age"))
	}
	if h.Get("Access-Control-Expose-Headers") != "X-Total-Count" {
		t.Errorf("expose-headers = %q", h.Get("Access-Control-Expose-Headers"))
	}
}

func TestCORSPlainOptionsStillRoutes(t *testing.T) {
	// An OPTIONS without Access-Control-Request-Method is not a preflight.
	e := corsEngine(middleware.CORSConfig{AllowOrigins: []string{"https://ok.example"}})

	rec := corsRequest(e, "OPTIONS", "https://ok.example", "")
	if rec.Body.String() != "route-options" {
		t.Errorf("body = %q, want the route to handle it", rec.Body)
	}
}

func TestCORSCredentials(t *testing.T) {
	e := corsEngine(middleware.CORSConfig{
		AllowOrigins:     []string{"https://ok.example"},
		AllowCredentials: true,
	})

	rec := corsRequest(e, "GET", "https://ok.example", "")

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://ok.example" {
		t.Errorf("allow-origin = %q, want the echoed origin, never *", got)
	}
	if rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Error("allow-credentials missing")
	}
}

func TestCORSWildcardWithCredentialsPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("wildcard + credentials accepted; browsers reject it and so should we")
		}
	}()
	middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins:     []string{"*"},
		AllowCredentials: true,
	})
}

func BenchmarkCORS(b *testing.B) {
	e := zarp.New()
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins: []string{"https://ok.example"},
	}))
	e.GET("/user/:id", func(c *zarp.Context) { c.Text(http.StatusOK, "ok") })

	req := httptest.NewRequest(http.MethodGet, "/user/42", nil)
	req.Header.Set("Origin", "https://ok.example")
	w := &nopWriter{}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		e.ServeHTTP(w, req)
	}
}

func BenchmarkRequestID(b *testing.B) {
	e := zarp.New()
	e.Use(middleware.RequestID())
	e.GET("/user/:id", func(c *zarp.Context) { c.Text(http.StatusOK, "ok") })
	benchServe(b, e, "GET", "/user/42")
}

// ---------------------------------------------------------------- max body size

func TestMaxBodySizeCapsEveryReader(t *testing.T) {
	var readErr error
	e := zarp.New()
	e.Use(middleware.MaxBodySize(16))
	e.POST("/x", func(c *zarp.Context) {
		_, readErr = io.ReadAll(c.Request.Body)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(strings.Repeat("a", 64)))
	e.ServeHTTP(rec, req)

	var maxErr *http.MaxBytesError
	if !errors.As(readErr, &maxErr) {
		t.Fatalf("read error = %v, want *http.MaxBytesError", readErr)
	}
	if maxErr.Limit != 16 {
		t.Errorf("limit = %d, want 16", maxErr.Limit)
	}
}

func TestMaxBodySizeLetsSmallBodiesThrough(t *testing.T) {
	var body string
	e := zarp.New()
	e.Use(middleware.MaxBodySize(16))
	e.POST("/x", func(c *zarp.Context) {
		b, err := io.ReadAll(c.Request.Body)
		if err != nil {
			t.Errorf("read: %v", err)
		}
		body = string(b)
	})

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("small")))

	if body != "small" {
		t.Errorf("body = %q", body)
	}
}

// ---------------------------------------------------------------- timeout

func TestTimeoutSetsADeadline(t *testing.T) {
	var (
		deadlineOK bool
		remaining  time.Duration
	)
	e := zarp.New()
	e.Use(middleware.Timeout(time.Minute))
	e.GET("/x", func(c *zarp.Context) {
		var dl time.Time
		dl, deadlineOK = c.Deadline()
		remaining = time.Until(dl)
	})

	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))

	if !deadlineOK {
		t.Fatal("handler saw no deadline")
	}
	if remaining <= 0 || remaining > time.Minute {
		t.Errorf("remaining = %v, want a value inside the minute", remaining)
	}
}

func TestTimeoutExpiresAndCancelsOnReturn(t *testing.T) {
	var (
		expired   error
		afterDone <-chan struct{}
	)
	e := zarp.New()
	e.Use(middleware.Timeout(time.Millisecond))
	e.GET("/x", func(c *zarp.Context) {
		<-c.Done()
		expired = c.Err()
		afterDone = c.Done()
	})

	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))

	if !errors.Is(expired, context.DeadlineExceeded) {
		t.Errorf("Err = %v, want DeadlineExceeded", expired)
	}
	select {
	case <-afterDone:
	default:
		t.Error("context still live after the chain returned")
	}
}

func TestLoggerEscapesControlCharactersInPath(t *testing.T) {
	// An unmatched route has no pattern, so the logger falls back to the
	// requested path — which net/http hands us URL-decoded, so %0A in the
	// request line arrives as a real newline. Written raw, that forges log
	// lines.
	var buf bytes.Buffer
	e := zarp.New()
	e.Use(middleware.LoggerWith(middleware.TextSink(&buf)))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.URL.Path = "/a\n[zarp] 2026/01/01 00:00:00 | 200 |  0s | 1.2.3.4 | GET     /admin"
	e.ServeHTTP(rec, req)

	line := buf.String()
	if strings.Count(line, "\n") != 1 {
		t.Errorf("log entry spans %d lines, want 1:\n%s", strings.Count(line, "\n"), line)
	}
	if !strings.Contains(line, `\n`) {
		t.Errorf("newline was not escaped: %q", line)
	}
}

func TestRequestIDValidatesGeneratorOutput(t *testing.T) {
	// A generator that returns something unfit for a header must not put it in
	// one: the middleware falls back rather than emitting it.
	var seen string
	e := zarp.New()
	e.Use(middleware.RequestIDWithConfig(middleware.RequestIDConfig{
		Generator: func() string { return "bad id\r\nX-Injected: evil" },
	}))
	e.GET("/x", func(c *zarp.Context) { seen = middleware.GetRequestID(c) })

	rec := serve(e, http.MethodGet, "/x")

	if strings.ContainsAny(seen, "\r\n ") {
		t.Errorf("emitted id %q carries characters a header cannot hold", seen)
	}
	if len(seen) != 32 {
		t.Errorf("id = %q, want the built-in generator's output", seen)
	}
	if got := rec.Header().Get(middleware.RequestIDHeader); got != seen {
		t.Errorf("header = %q, stored = %q", got, seen)
	}
}

func TestRequestIDRejectsAMalformedHeaderName(t *testing.T) {
	for _, name := range []string{"X Request Id", "X-Request-Id:", "X-Request\nId", `X-"Id"`} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("header name %q was accepted", name)
				}
			}()
			middleware.RequestIDWithConfig(middleware.RequestIDConfig{Header: name})
		})
	}
}

func TestMaxBodySizeRejectsANonPositiveLimit(t *testing.T) {
	for _, n := range []int64{0, -1} {
		t.Run(strconv.FormatInt(n, 10), func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("MaxBodySize(%d) was accepted; it would reject every body", n)
				}
			}()
			middleware.MaxBodySize(n)
		})
	}
}

func TestRecoveryEscapesThePanicValue(t *testing.T) {
	var buf bytes.Buffer
	e := zarp.New()
	e.Use(middleware.RecoveryWithWriter(&buf))
	e.GET("/x", func(*zarp.Context) {
		panic("boom\n[zarp] panic recovered: GET /elsewhere")
	})

	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))

	// The stack trace has newlines of its own; what matters is that the panic
	// value did not contribute one.
	header, _, _ := strings.Cut(buf.String(), "\n")
	if !strings.Contains(buf.String(), `boom\n`) {
		t.Errorf("panic value was not escaped:\n%s", buf.String())
	}
	if strings.Contains(header, "elsewhere") {
		t.Errorf("panic value broke out of its line: %q", header)
	}
}
