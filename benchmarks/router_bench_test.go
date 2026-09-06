// Package benchmarks holds the perf benchmarks for gomicro. It lives outside
// the root package so `go test ./...` stays fast; run it deliberately with
//
//	go test -bench=. -benchmem ./benchmarks/...
package benchmarks

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/subhanjanops/gomicro"
)

// githubAPI is a representative route set modelled on the GitHub API, the same
// shape used by the httprouter/gin benchmarks: a mix of static, single-param,
// multi-param and catch-all routes, nested several levels deep.
//
// It is curated to be conflict-free under gomicro's registration rules, which
// reject a static segment competing with a wildcard at the same position (see
// router.go). That is why, unlike the original GitHub set, there is no
// "/gists/public" sitting beside "/gists/:gist".
var githubAPI = []string{
	// static
	"/",
	"/health",
	"/version",
	"/metrics",
	"/emojis",
	"/events",
	"/feeds",
	"/rate_limit",
	"/gists",
	"/notifications",
	"/user",
	"/user/repos",
	"/user/followers",
	"/user/following",
	"/user/emails",
	"/user/keys",
	"/user/starred",
	"/user/subscriptions",
	"/user/issues",
	"/user/teams",

	// single param
	"/users/:user",
	"/users/:user/repos",
	"/users/:user/followers",
	"/users/:user/following",
	"/users/:user/gists",
	"/users/:user/keys",
	"/users/:user/events",
	"/users/:user/received_events",
	"/users/:user/subscriptions",
	"/users/:user/orgs",
	"/orgs/:org",
	"/orgs/:org/members",
	"/orgs/:org/repos",
	"/orgs/:org/teams",
	"/orgs/:org/issues",
	"/teams/:team",
	"/teams/:team/members",
	"/teams/:team/repos",
	"/gists/:gist",
	"/gists/:gist/comments",
	"/gists/:gist/forks",
	"/gists/:gist/star",

	// multi param
	"/repos/:owner/:repo",
	"/repos/:owner/:repo/contributors",
	"/repos/:owner/:repo/languages",
	"/repos/:owner/:repo/teams",
	"/repos/:owner/:repo/tags",
	"/repos/:owner/:repo/branches",
	"/repos/:owner/:repo/branches/:branch",
	"/repos/:owner/:repo/collaborators/:user",
	"/repos/:owner/:repo/comments/:id",
	"/repos/:owner/:repo/commits/:sha",
	"/repos/:owner/:repo/commits/:sha/comments",
	"/repos/:owner/:repo/issues/:number",
	"/repos/:owner/:repo/issues/:number/comments",
	"/repos/:owner/:repo/issues/:number/labels",
	"/repos/:owner/:repo/issues/:number/events",
	"/repos/:owner/:repo/pulls/:number",
	"/repos/:owner/:repo/pulls/:number/files",
	"/repos/:owner/:repo/pulls/:number/commits",
	"/repos/:owner/:repo/pulls/:number/merge",
	"/repos/:owner/:repo/releases/:id",
	"/repos/:owner/:repo/releases/:id/assets",
	"/repos/:owner/:repo/keys/:id",
	"/repos/:owner/:repo/labels/:name",
	"/repos/:owner/:repo/milestones/:number",
	"/repos/:owner/:repo/subscription",
	"/repos/:owner/:repo/stargazers",
	"/networks/:owner/:repo/events",
	"/legacy/issues/search/:owner/:repository/:state/:keyword",

	// catch-all
	"/static/*filepath",
	"/repos/:owner/:repo/contents/*path",
	"/repos/:owner/:repo/git/refs/*ref",
}

// Request paths exercising each shape of the tree.
var (
	staticReqs = []string{
		"/",
		"/health",
		"/user/repos",
		"/user/subscriptions",
		"/rate_limit",
	}
	paramReqs = []string{
		"/users/golang",
		"/users/golang/repos",
		"/orgs/golang/members",
		"/gists/aa5a315d61ae9438b18d/comments",
		"/teams/42/members",
	}
	deepParamReqs = []string{
		"/repos/golang/go",
		"/repos/golang/go/issues/1234/comments",
		"/repos/golang/go/pulls/9876/files",
		"/repos/golang/go/commits/deadbeef/comments",
		"/legacy/issues/search/golang/go/open/generics",
	}
	catchAllReqs = []string{
		"/static/css/site.min.css",
		"/repos/golang/go/contents/src/net/http/server.go",
		"/repos/golang/go/git/refs/heads/master",
	}
	allReqs = concat(staticReqs, paramReqs, deepParamReqs, catchAllReqs)
)

func concat(sets ...[]string) []string {
	var out []string
	for _, s := range sets {
		out = append(out, s...)
	}
	return out
}

func noopHandler(*gomicro.Context) {}

func newRouter(b *testing.B) *gomicro.Router {
	b.Helper()
	r := new(gomicro.Router)
	handlers := []gomicro.HandlerFunc{noopHandler}
	for _, path := range githubAPI {
		r.Insert(http.MethodGet, path, handlers)
	}
	return r
}

// benchLookup drives the match path with a reusable param buffer, which is how
// Engine will call it once Context is pooled. This is the number that matters:
// it must report 0 allocs/op.
func benchLookup(b *testing.B, reqs []string) {
	r := newRouter(b)
	buf := make([]gomicro.Param, 0, r.MaxParams())

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, path := range reqs {
			handlers, _, _ := r.Lookup(http.MethodGet, path, buf[:0])
			if handlers == nil {
				b.Fatalf("no route matched %q", path)
			}
		}
	}
}

func BenchmarkGomicroStatic(b *testing.B)    { benchLookup(b, staticReqs) }
func BenchmarkGomicroParam(b *testing.B)     { benchLookup(b, paramReqs) }
func BenchmarkGomicroDeepParam(b *testing.B) { benchLookup(b, deepParamReqs) }
func BenchmarkGomicroCatchAll(b *testing.B)  { benchLookup(b, catchAllReqs) }
func BenchmarkGomicroAll(b *testing.B)       { benchLookup(b, allReqs) }

// BenchmarkGomicroAllUnpooled is the same work without a caller-supplied
// buffer: it isolates the cost of the params slice the pool is there to remove.
func BenchmarkGomicroAllUnpooled(b *testing.B) {
	r := newRouter(b)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, path := range allReqs {
			handlers, _, _ := r.Lookup(http.MethodGet, path, nil)
			if handlers == nil {
				b.Fatalf("no route matched %q", path)
			}
		}
	}
}

// --- stdlib baseline -------------------------------------------------------
//
// net/http.ServeMux over the same route set. This is not apples to apples:
// ServeMux resolves a whole *http.Request (host, method, path cleaning) rather
// than a path string, and its wildcards are segment-based. It is here as the
// stdlib floor the design goal in CLAUDE.md refers to, not as a strict
// like-for-like comparison.

// muxPattern converts a gomicro route to its ServeMux equivalent:
// ":name" -> "{name}", "*name" -> "{name...}".
func muxPattern(method, path string) string {
	var sb strings.Builder
	sb.WriteString(method)
	sb.WriteByte(' ')

	for _, seg := range strings.Split(path, "/") {
		if seg == "" {
			continue
		}
		sb.WriteByte('/')
		switch seg[0] {
		case ':':
			sb.WriteString("{" + seg[1:] + "}")
		case '*':
			sb.WriteString("{" + seg[1:] + "...}")
		default:
			sb.WriteString(seg)
		}
	}
	if strings.HasSuffix(path, "/") {
		sb.WriteByte('/')
	}
	return sb.String()
}

func newServeMux(b *testing.B) *http.ServeMux {
	b.Helper()
	mux := http.NewServeMux()
	h := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	for _, path := range githubAPI {
		mux.Handle(muxPattern(http.MethodGet, path), h)
	}
	return mux
}

// benchServeMux pre-builds the requests so the loop measures matching only,
// not request construction.
func benchServeMux(b *testing.B, paths []string) {
	mux := newServeMux(b)
	reqs := make([]*http.Request, len(paths))
	for i, p := range paths {
		reqs[i] = httptest.NewRequest(http.MethodGet, p, nil)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, req := range reqs {
			if h, _ := mux.Handler(req); h == nil {
				b.Fatalf("no route matched %q", req.URL.Path)
			}
		}
	}
}

func BenchmarkServeMuxStatic(b *testing.B)    { benchServeMux(b, staticReqs) }
func BenchmarkServeMuxParam(b *testing.B)     { benchServeMux(b, paramReqs) }
func BenchmarkServeMuxDeepParam(b *testing.B) { benchServeMux(b, deepParamReqs) }
func BenchmarkServeMuxCatchAll(b *testing.B)  { benchServeMux(b, catchAllReqs) }
func BenchmarkServeMuxAll(b *testing.B)       { benchServeMux(b, allReqs) }

// BenchmarkGomicroInsert covers tree construction. Setup time is allowed to
// allocate; this is here to catch a pathological regression, not to be tuned.
func BenchmarkGomicroInsert(b *testing.B) {
	handlers := []gomicro.HandlerFunc{noopHandler}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := new(gomicro.Router)
		for _, path := range githubAPI {
			r.Insert(http.MethodGet, path, handlers)
		}
	}
}
