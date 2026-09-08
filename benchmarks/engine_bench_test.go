// Package benchmarks holds gomicro's end-to-end performance suite.
//
// It is a separate package so `go test ./...` stays fast, and it imports
// gomicro exactly as a user would — registration goes through the exported verb
// methods, so nothing here can measure a shortcut that real code cannot take.
//
//	go test -bench=. -benchmem ./benchmarks/...
package benchmarks

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/subhanjanops/gomicro"
)

// nopWriter measures the framework rather than httptest's buffer growth. It
// implements io.StringWriter because net/http's own response does: without it
// io.WriteString falls back to a []byte conversion and the benchmark measures
// an allocation production never pays.
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

func engine() *gomicro.Engine {
	e := gomicro.New()
	e.GET("/ping", func(c *gomicro.Context) { c.Text(http.StatusOK, "pong") })
	e.GET("/user/:id", func(c *gomicro.Context) { c.Text(http.StatusOK, c.Param("id")) })
	e.GET("/j", func(c *gomicro.Context) {
		c.JSON(http.StatusOK, map[string]string{"message": "pong"})
	})
	return e
}

// mux is the net/http equivalent, doing the same work per request.
func mux() *http.ServeMux {
	m := http.NewServeMux()
	m.HandleFunc("GET /ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("pong"))
	})
	m.HandleFunc("GET /user/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(r.PathValue("id")))
	})
	return m
}

func BenchmarkEngineStatic(b *testing.B)  { benchServe(b, engine(), "GET", "/ping") }
func BenchmarkNetHTTPStatic(b *testing.B) { benchServe(b, mux(), "GET", "/ping") }
func BenchmarkEngineParam(b *testing.B)   { benchServe(b, engine(), "GET", "/user/42") }
func BenchmarkNetHTTPParam(b *testing.B)  { benchServe(b, mux(), "GET", "/user/42") }
func BenchmarkEngineJSON(b *testing.B)    { benchServe(b, engine(), "GET", "/j") }
func BenchmarkEngine404(b *testing.B)     { benchServe(b, engine(), "GET", "/nope") }

// BenchmarkEngineStringFmt is the same route as BenchmarkEngineParam written
// with String rather than Text. The difference is the fmt argument boxing,
// which is the handler's allocation, not the engine's.
func BenchmarkEngineStringFmt(b *testing.B) {
	e := gomicro.New()
	e.GET("/user/:id", func(c *gomicro.Context) {
		c.String(http.StatusOK, "%s", c.Param("id"))
	})
	benchServe(b, e, "GET", "/user/42")
}

func BenchmarkEngine405(b *testing.B) {
	e := gomicro.New()
	e.HandleMethodNotAllowed = true
	e.GET("/x", func(c *gomicro.Context) {})
	e.PUT("/x", func(c *gomicro.Context) {})
	benchServe(b, e, "POST", "/x")
}

func BenchmarkEngineRedirect(b *testing.B) {
	e := gomicro.New()
	e.GET("/dir/", func(c *gomicro.Context) {})
	benchServe(b, e, "GET", "/dir")
}

func BenchmarkEngineParallel(b *testing.B) {
	e := engine()
	req := httptest.NewRequest("GET", "/user/42", nil)
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		w := &nopWriter{}
		for pb.Next() {
			e.ServeHTTP(w, req)
		}
	})
}

// ---------------------------------------------------------------- middleware

func chainOf(n int) *gomicro.Engine {
	e := gomicro.New()
	for range n {
		e.Use(func(c *gomicro.Context) { c.Next() })
	}
	e.GET("/x", func(c *gomicro.Context) { c.Text(http.StatusOK, "ok") })
	return e
}

func BenchmarkChain0(b *testing.B)  { benchServe(b, chainOf(0), "GET", "/x") }
func BenchmarkChain1(b *testing.B)  { benchServe(b, chainOf(1), "GET", "/x") }
func BenchmarkChain3(b *testing.B)  { benchServe(b, chainOf(3), "GET", "/x") }
func BenchmarkChain10(b *testing.B) { benchServe(b, chainOf(10), "GET", "/x") }

func BenchmarkGroupedRoute(b *testing.B) {
	e := gomicro.New()
	e.Use(func(c *gomicro.Context) { c.Next() })
	v1 := e.Group("/api/v1", func(c *gomicro.Context) { c.Next() })
	v1.GET("/users/:id", func(c *gomicro.Context) { c.Text(http.StatusOK, c.Param("id")) })
	benchServe(b, e, "GET", "/api/v1/users/42")
}
