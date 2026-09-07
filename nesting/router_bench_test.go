package nesting

import (
	"net/http"
	"testing"
)

// The route set is the shape of a real API: a static core plus params and one
// catch-all. Each benchmark pairs a gomicro Lookup with the equivalent
// net/http.ServeMux match, so the overhead claim is measured, not asserted.
var benchRoutes = []struct{ gomicro, mux string }{
	{"/", "/{$}"},
	{"/events", "/events"},
	{"/notifications", "/notifications"},
	{"/search/code", "/search/code"},
	{"/search/issues", "/search/issues"},
	{"/users/:user", "/users/{user}"},
	{"/users/:user/repos", "/users/{user}/repos"},
	{"/orgs/:org/members", "/orgs/{org}/members"},
	{"/repos/:owner/:repo", "/repos/{owner}/{repo}"},
	{"/repos/:owner/:repo/issues", "/repos/{owner}/{repo}/issues"},
	{"/repos/:owner/:repo/issues/:number", "/repos/{owner}/{repo}/issues/{number}"},
	{"/repos/:owner/:repo/contents/*path", "/repos/{owner}/{repo}/contents/{path...}"},
}

func benchRouter() *Router {
	r := &Router{}
	for _, rt := range benchRoutes {
		r.addRoute(http.MethodGet, rt.gomicro, []HandlerFunc{func(*Context) {}})
	}
	return r
}

func benchMux() *http.ServeMux {
	m := http.NewServeMux()
	for _, rt := range benchRoutes {
		m.Handle("GET "+rt.mux, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	}
	return m
}

// sink keeps the compiler from eliminating the calls being measured.
var (
	sinkHandlers []HandlerFunc
	sinkHandler  http.Handler
)

func benchLookup(b *testing.B, path string) {
	r := benchRouter()
	ps := make(Params, 0, r.maxParams)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		ps = ps[:0]
		sinkHandlers, _, _ = r.Lookup(http.MethodGet, path, &ps)
	}
}

func benchMuxLookup(b *testing.B, path string) {
	m := benchMux()
	req, err := http.NewRequest(http.MethodGet, "http://x"+path, nil)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		sinkHandler, _ = m.Handler(req)
	}
}

func BenchmarkRouterStatic(b *testing.B)   { benchLookup(b, "/search/issues") }
func BenchmarkServeMuxStatic(b *testing.B) { benchMuxLookup(b, "/search/issues") }
func BenchmarkRouterParam(b *testing.B)    { benchLookup(b, "/users/octocat") }
func BenchmarkServeMuxParam(b *testing.B)  { benchMuxLookup(b, "/users/octocat") }
func BenchmarkRouterParams(b *testing.B)   { benchLookup(b, "/repos/golang/go/issues/42") }
func BenchmarkServeMuxParams(b *testing.B) { benchMuxLookup(b, "/repos/golang/go/issues/42") }
func BenchmarkRouterCatchAll(b *testing.B) { benchLookup(b, "/repos/golang/go/contents/src/a.go") }
func BenchmarkServeMuxCatchAll(b *testing.B) {
	benchMuxLookup(b, "/repos/golang/go/contents/src/a.go")
}
func BenchmarkRouterMiss(b *testing.B)   { benchLookup(b, "/does/not/exist") }
func BenchmarkServeMuxMiss(b *testing.B) { benchMuxLookup(b, "/does/not/exist") }
