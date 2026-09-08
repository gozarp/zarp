package middleware_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/subhanjanops/gomicro"
	"github.com/subhanjanops/gomicro/middleware"
)

func serve(e *gomicro.Engine, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

// ---------------------------------------------------------------- Recovery

func TestRecoveryTurnsPanicInto500(t *testing.T) {
	var log bytes.Buffer
	e := gomicro.New()
	e.Use(middleware.RecoveryWithWriter(&log))
	e.GET("/boom", func(c *gomicro.Context) { panic("handler exploded") })

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
	e := gomicro.New()
	e.Use(middleware.RecoveryWithWriter(nil))
	e.GET("/boom",
		func(c *gomicro.Context) { ran = append(ran, "first"); c.Next() },
		func(c *gomicro.Context) { panic("x") },
		func(c *gomicro.Context) { ran = append(ran, "after-panic") },
	)

	serve(e, "GET", "/boom")

	if got := strings.Join(ran, " "); got != "first" {
		t.Errorf("ran = %q, want nothing after the panic", got)
	}
}

func TestRecoveryKeepsServerUsable(t *testing.T) {
	// A recovered panic unwinds inside Next, so the Context still returns to
	// the pool and later requests are unaffected.
	e := gomicro.New()
	e.Use(middleware.RecoveryWithWriter(nil))
	e.GET("/boom", func(c *gomicro.Context) { panic("x") })
	e.GET("/ok/:id", func(c *gomicro.Context) { c.Text(http.StatusOK, c.Param("id")) })

	for range 5 {
		serve(e, "GET", "/boom")
		if got := serve(e, "GET", "/ok/42").Body.String(); got != "42" {
			t.Fatalf("after a panic, /ok/42 = %q", got)
		}
	}
}

func TestRecoveryRepanicsErrAbortHandler(t *testing.T) {
	e := gomicro.New()
	e.Use(middleware.RecoveryWithWriter(nil))
	e.GET("/abort", func(c *gomicro.Context) { panic(http.ErrAbortHandler) })

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
	e := gomicro.New()
	e.Use(middleware.RecoveryWithWriter(&log))
	e.GET("/gone", func(c *gomicro.Context) {
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
	e := gomicro.New()
	e.Use(middleware.RecoveryWithHandler(func(c *gomicro.Context, err any) {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable,
			map[string]string{"error": "try later"})
	}))
	e.GET("/boom", func(c *gomicro.Context) { panic("x") })

	rec := serve(e, "GET", "/boom")

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("code = %d, want 503", rec.Code)
	}
	if got := rec.Body.String(); got != `{"error":"try later"}` {
		t.Errorf("body = %q", got)
	}
}

func TestRecoveryPassesThroughNormalRequests(t *testing.T) {
	e := gomicro.New()
	e.Use(middleware.RecoveryWithWriter(nil))
	e.GET("/ok", func(c *gomicro.Context) { c.Text(http.StatusOK, "fine") })

	rec := serve(e, "GET", "/ok")
	if rec.Code != http.StatusOK || rec.Body.String() != "fine" {
		t.Errorf("code=%d body=%q", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------- Logger

func TestLoggerWritesRequestDetails(t *testing.T) {
	var log bytes.Buffer
	e := gomicro.New()
	e.Use(middleware.LoggerWithWriter(&log))
	e.GET("/user/:id", func(c *gomicro.Context) { c.Text(http.StatusCreated, "hi") })

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
	e := gomicro.New()
	e.Use(middleware.LoggerWithWriter(&log))
	e.Use(func(c *gomicro.Context) {
		c.Next()
		c.Status(http.StatusTeapot) // changed after the handler
	})
	e.GET("/x", func(c *gomicro.Context) { c.Status(http.StatusOK) })

	serve(e, "GET", "/x")

	if !strings.Contains(log.String(), "418") {
		t.Errorf("log = %q, want the final status", log.String())
	}
}

func TestLoggerSkipPaths(t *testing.T) {
	var log bytes.Buffer
	e := gomicro.New()
	e.Use(middleware.LoggerWithConfig(middleware.LoggerConfig{
		Sink:      middleware.TextSink(&log),
		SkipPaths: []string{"/healthz"},
	}))
	e.GET("/healthz", func(c *gomicro.Context) { c.Text(http.StatusOK, "ok") })
	e.GET("/real", func(c *gomicro.Context) { c.Text(http.StatusOK, "ok") })

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
	e := gomicro.New()
	e.Use(middleware.LoggerWith(middleware.SinkFunc(func(e middleware.Entry) {
		fmt.Fprintf(&log, "%s %s %s\n", e.Method, e.Path, e.URL)
	})))
	e.GET("/user/:id", func(c *gomicro.Context) {})

	serve(e, "GET", "/user/42?q=go")

	if got := log.String(); got != "GET /user/:id /user/42?q=go\n" {
		t.Errorf("log = %q", got)
	}
}

func TestLoggerEntryFields(t *testing.T) {
	var got middleware.Entry
	e := gomicro.New()
	e.Use(middleware.LoggerWith(middleware.SinkFunc(func(e middleware.Entry) { got = e })))
	e.GET("/user/:id", func(c *gomicro.Context) { c.Text(http.StatusOK, "hello") })

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
	e := gomicro.New()
	e.Use(middleware.LoggerWithWriter(&log))
	e.GET("/known", func(c *gomicro.Context) {})

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
	e := gomicro.New()
	e.Use(middleware.LoggerWithWriter(&log), middleware.RecoveryWithWriter(nil))
	e.GET("/boom", func(c *gomicro.Context) { panic("x") })

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
	e := gomicro.New()
	e.Use(middleware.LoggerWithWriter(io.Discard))
	e.GET("/user/:id", func(c *gomicro.Context) { c.Text(http.StatusOK, "ok") })
	benchServe(b, e, "GET", "/user/42")
}

func BenchmarkRecovery(b *testing.B) {
	e := gomicro.New()
	e.Use(middleware.RecoveryWithWriter(nil))
	e.GET("/user/:id", func(c *gomicro.Context) { c.Text(http.StatusOK, "ok") })
	benchServe(b, e, "GET", "/user/42")
}

func BenchmarkNoMiddleware(b *testing.B) {
	e := gomicro.New()
	e.GET("/user/:id", func(c *gomicro.Context) { c.Text(http.StatusOK, "ok") })
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
	e := gomicro.New()
	e.Use(middleware.RequestID())
	e.GET("/x", func(c *gomicro.Context) { seen = middleware.GetRequestID(c) })

	rec := serve(e, "GET", "/x")

	if len(seen) != 32 {
		t.Errorf("id = %q, want 32 hex chars", seen)
	}
	if got := rec.Header().Get(middleware.RequestIDHeader); got != seen {
		t.Errorf("header = %q, want it echoed as %q", got, seen)
	}
}

func TestRequestIDIsUniquePerRequest(t *testing.T) {
	e := gomicro.New()
	e.Use(middleware.RequestID())
	e.GET("/x", func(c *gomicro.Context) {})

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
	e := gomicro.New()
	e.Use(middleware.RequestID())
	e.GET("/x", func(c *gomicro.Context) { seen = middleware.GetRequestID(c) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
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
			e := gomicro.New()
			e.Use(middleware.RequestID())
			e.GET("/x", func(c *gomicro.Context) { seen = middleware.GetRequestID(c) })

			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/x", nil)
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
	no := false
	var seen string
	e := gomicro.New()
	e.Use(middleware.RequestIDWithConfig(middleware.RequestIDConfig{TrustInbound: &no}))
	e.GET("/x", func(c *gomicro.Context) { seen = middleware.GetRequestID(c) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set(middleware.RequestIDHeader, "from-client")
	e.ServeHTTP(rec, req)

	if seen == "from-client" {
		t.Error("inbound id used while TrustInbound is false")
	}
}

func TestRequestIDCustomHeaderAndGenerator(t *testing.T) {
	e := gomicro.New()
	e.Use(middleware.RequestIDWithConfig(middleware.RequestIDConfig{
		Header:    "X-Trace",
		Generator: func() string { return "fixed" },
	}))
	e.GET("/x", func(c *gomicro.Context) {})

	rec := serve(e, "GET", "/x")
	if got := rec.Header().Get("X-Trace"); got != "fixed" {
		t.Errorf("X-Trace = %q", got)
	}
}

// ---------------------------------------------------------------- CORS

func corsRequest(e *gomicro.Engine, method, origin, preflightFor string) *httptest.ResponseRecorder {
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

func corsEngine(cfg middleware.CORSConfig) *gomicro.Engine {
	e := gomicro.New()
	e.Use(middleware.CORSWithConfig(cfg))
	e.GET("/x", func(c *gomicro.Context) { c.Text(http.StatusOK, "ok") })
	e.OPTIONS("/x", func(c *gomicro.Context) { c.Text(http.StatusOK, "route-options") })
	return e
}

func TestCORSAllowsAnyOriginByDefault(t *testing.T) {
	e := gomicro.New()
	e.Use(middleware.CORS())
	e.GET("/x", func(c *gomicro.Context) { c.Text(http.StatusOK, "ok") })

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
	e := gomicro.New()
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins: []string{"https://ok.example"},
	}))
	e.GET("/user/:id", func(c *gomicro.Context) { c.Text(http.StatusOK, "ok") })

	req := httptest.NewRequest("GET", "/user/42", nil)
	req.Header.Set("Origin", "https://ok.example")
	w := &nopWriter{}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		e.ServeHTTP(w, req)
	}
}

func BenchmarkRequestID(b *testing.B) {
	e := gomicro.New()
	e.Use(middleware.RequestID())
	e.GET("/user/:id", func(c *gomicro.Context) { c.Text(http.StatusOK, "ok") })
	benchServe(b, e, "GET", "/user/42")
}
