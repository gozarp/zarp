package middleware_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/subhanjanops/gomicro"
	"github.com/subhanjanops/gomicro/middleware"
)

// engineWithSink serves one request through a Logger using sink.
func engineWithSink(t *testing.T, sink middleware.Sink, status int, target string) {
	t.Helper()
	e := gomicro.New()
	e.Use(middleware.LoggerWith(sink))
	e.GET("/user/:id", func(c *gomicro.Context) { c.Text(status, "body") })
	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", target, nil))
}

// ---------------------------------------------------------------- Entry.Level

func TestEntryLevel(t *testing.T) {
	tests := []struct {
		status int
		want   slog.Level
	}{
		{http.StatusOK, slog.LevelInfo},
		{http.StatusMovedPermanently, slog.LevelInfo},
		{http.StatusBadRequest, slog.LevelWarn},
		{http.StatusNotFound, slog.LevelWarn},
		{http.StatusInternalServerError, slog.LevelError},
		{http.StatusBadGateway, slog.LevelError},
	}
	for _, tc := range tests {
		if got := (middleware.Entry{Status: tc.status}).Level(); got != tc.want {
			t.Errorf("status %d -> %v, want %v", tc.status, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------- slog

func TestSlogSink(t *testing.T) {
	var out bytes.Buffer
	l := slog.New(slog.NewJSONHandler(&out, &slog.HandlerOptions{Level: slog.LevelDebug}))

	engineWithSink(t, middleware.SlogSink(l), http.StatusOK, "/user/42")

	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v (%q)", err, out.String())
	}
	if got["msg"] != "request" || got["path"] != "/user/:id" {
		t.Errorf("got %v", got)
	}
	if got["level"] != "INFO" {
		t.Errorf("level = %v", got["level"])
	}
}

func TestSlogSinkSeverity(t *testing.T) {
	var out bytes.Buffer
	l := slog.New(slog.NewJSONHandler(&out, &slog.HandlerOptions{Level: slog.LevelDebug}))

	engineWithSink(t, middleware.SlogSink(l), http.StatusInternalServerError, "/user/42")

	if !strings.Contains(out.String(), `"level":"ERROR"`) {
		t.Errorf("log = %q, want ERROR for a 5xx", out.String())
	}
}

// ---------------------------------------------------------------- custom

// zerologLike stands in for any third-party logger: the adapter is written by
// the application, and neither package knows about the other.
type zerologLike struct{ lines []string }

func (z *zerologLike) event(level, msg string) { z.lines = append(z.lines, level+" "+msg) }

func TestCustomAdapter(t *testing.T) {
	var z zerologLike
	sink := middleware.SinkFunc(func(e middleware.Entry) {
		z.event(e.Level().String(), e.Method+" "+e.Path)
	})

	engineWithSink(t, sink, http.StatusOK, "/user/42")

	if len(z.lines) != 1 || z.lines[0] != "INFO GET /user/:id" {
		t.Errorf("lines = %v", z.lines)
	}
}

func TestSinkFuncSatisfiesSink(t *testing.T) {
	var _ middleware.Sink = middleware.SinkFunc(func(middleware.Entry) {})
	var _ middleware.Sink = middleware.TextSink(nil)
	var _ middleware.Sink = middleware.SlogSink(slog.Default())
}

// ---------------------------------------------------------------- benchmarks

func benchSink(b *testing.B, sink middleware.Sink) {
	e := gomicro.New()
	e.Use(middleware.LoggerWith(sink))
	e.GET("/user/:id", func(c *gomicro.Context) { c.Text(http.StatusOK, "ok") })
	benchServe(b, e, "GET", "/user/42")
}

func BenchmarkSinkText(b *testing.B) {
	benchSink(b, middleware.TextSink(io.Discard))
}

func BenchmarkSinkSlog(b *testing.B) {
	benchSink(b, middleware.SlogSink(slog.New(slog.NewJSONHandler(io.Discard, nil))))
}

func TestDefaultSinkIsDependencyFree(t *testing.T) {
	// A zero LoggerConfig must work without any logging library configured.
	e := gomicro.New()
	e.Use(middleware.LoggerWithConfig(middleware.LoggerConfig{}))
	e.GET("/user/:id", func(c *gomicro.Context) { c.Text(http.StatusOK, "ok") })

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest("GET", "/user/42", nil))

	if rec.Body.String() != "ok" {
		t.Errorf("body = %q", rec.Body)
	}
}
