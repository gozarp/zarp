package gomicro

import (
	"strings"
	"testing"
)

// fakeHandlers builds a distinguishable handler chain. Handlers are compared by
// length and by the marker captured in the closure, never by identity, since
// func values are not comparable in Go.
func fakeHandlers(marker string) []HandlerFunc {
	return []HandlerFunc{func(*Context) { _ = marker }}
}

// newTree registers paths in a single method tree and returns its root.
func newTree(t *testing.T, paths ...string) *node {
	t.Helper()
	r := new(Router)
	for _, p := range paths {
		r.insert("GET", p, fakeHandlers(p))
	}
	n := r.tree("GET")
	if n == nil {
		t.Fatal("no tree registered for GET")
	}
	return n
}

// requireRoute asserts that path matches, and that the params match want
// exactly (order included).
func requireRoute(t *testing.T, n *node, path string, want ...Param) {
	t.Helper()
	handlers, params, tsr := n.getValue(path)
	if handlers == nil {
		t.Fatalf("getValue(%q): no handlers found (tsr=%v)", path, tsr)
	}
	if tsr {
		t.Errorf("getValue(%q): tsr = true, want false on a direct match", path)
	}
	if len(params) != len(want) {
		t.Fatalf("getValue(%q): params = %v, want %v", path, params, want)
	}
	for i := range want {
		if params[i] != want[i] {
			t.Errorf("getValue(%q): params[%d] = %+v, want %+v", path, i, params[i], want[i])
		}
	}
}

// requireNoRoute asserts that path does not match, and checks the trailing
// slash recommendation.
func requireNoRoute(t *testing.T, n *node, path string, wantTSR bool) {
	t.Helper()
	handlers, _, tsr := n.getValue(path)
	if handlers != nil {
		t.Fatalf("getValue(%q): matched, want no match", path)
	}
	if tsr != wantTSR {
		t.Errorf("getValue(%q): tsr = %v, want %v", path, tsr, wantTSR)
	}
}

func requirePanic(t *testing.T, substr string, fn func()) {
	t.Helper()
	defer func() {
		rec := recover()
		if rec == nil {
			t.Fatalf("expected panic containing %q, got none", substr)
		}
		msg, ok := rec.(string)
		if !ok {
			t.Fatalf("expected string panic, got %T: %v", rec, rec)
		}
		if !strings.Contains(msg, substr) {
			t.Errorf("panic = %q, want it to contain %q", msg, substr)
		}
	}()
	fn()
}

func TestStaticRoutes(t *testing.T) {
	paths := []string{
		"/",
		"/hi",
		"/contact",
		"/co",
		"/c",
		"/a",
		"/ab",
		"/doc/",
		"/doc/go_faq.html",
		"/doc/go1.html",
		"/α",
		"/β",
	}
	n := newTree(t, paths...)

	for _, p := range paths {
		requireRoute(t, n, p)
	}

	for _, p := range []string{"/noexist", "/con", "/cona", "/ab/", "/doc/go"} {
		if handlers, _, _ := n.getValue(p); handlers != nil {
			t.Errorf("getValue(%q): matched, want no match", p)
		}
	}
}

// A shared prefix must not let a shorter route swallow a longer one: matching
// walks the whole path, it does not stop at the first node carrying handlers.
func TestStaticLongestMatchWins(t *testing.T) {
	n := newTree(t, "/users", "/users/list", "/users/list/all")

	for _, p := range []string{"/users", "/users/list", "/users/list/all"} {
		handlers, _, _ := n.getValue(p)
		if handlers == nil {
			t.Fatalf("getValue(%q): no match", p)
		}
	}
	requireNoRoute(t, n, "/users/lis", false)
}

func TestSingleParam(t *testing.T) {
	n := newTree(t, "/users/:id")

	requireRoute(t, n, "/users/42", Param{Key: "id", Value: "42"})
	requireRoute(t, n, "/users/gopher", Param{Key: "id", Value: "gopher"})

	// An empty segment is not a param value.
	requireNoRoute(t, n, "/users/", false)
	// A param never spans a '/'.
	requireNoRoute(t, n, "/users/42/posts", false)
}

func TestMultipleParams(t *testing.T) {
	n := newTree(t,
		"/users/:uid/posts/:pid",
		"/repos/:owner/:repo/issues/:number",
	)

	requireRoute(t, n, "/users/7/posts/99",
		Param{Key: "uid", Value: "7"},
		Param{Key: "pid", Value: "99"},
	)
	requireRoute(t, n, "/repos/golang/go/issues/1234",
		Param{Key: "owner", Value: "golang"},
		Param{Key: "repo", Value: "go"},
		Param{Key: "number", Value: "1234"},
	)
}

// Params in the middle of a segment ("/file-:name") are valid: only the
// wildcard part is captured.
func TestParamWithStaticPrefixInSegment(t *testing.T) {
	n := newTree(t, "/file-:name")
	requireRoute(t, n, "/file-report.pdf", Param{Key: "name", Value: "report.pdf"})
	requireNoRoute(t, n, "/file-", false)
}

func TestParamsGet(t *testing.T) {
	n := newTree(t, "/users/:uid/posts/:pid")
	_, params, _ := n.getValue("/users/7/posts/99")

	ps := Params(params)
	if got, ok := ps.Get("pid"); !ok || got != "99" {
		t.Errorf(`Get("pid") = %q, %v; want "99", true`, got, ok)
	}
	if got, ok := ps.Get("nope"); ok || got != "" {
		t.Errorf(`Get("nope") = %q, %v; want "", false`, got, ok)
	}
	if got := ps.ByName("uid"); got != "7" {
		t.Errorf(`ByName("uid") = %q, want "7"`, got)
	}
}

func TestCatchAll(t *testing.T) {
	n := newTree(t, "/src/*filepath")

	// The catch-all value keeps its leading slash.
	requireRoute(t, n, "/src/", Param{Key: "filepath", Value: "/"})
	requireRoute(t, n, "/src/file.go", Param{Key: "filepath", Value: "/file.go"})
	requireRoute(t, n, "/src/some/deep/path.go", Param{Key: "filepath", Value: "/some/deep/path.go"})

	// "/src" itself has no handler, but "/src/" does: recommend the slash.
	requireNoRoute(t, n, "/src", true)
}

func TestCatchAllAfterParam(t *testing.T) {
	n := newTree(t, "/repos/:owner/contents/*filepath")

	requireRoute(t, n, "/repos/golang/contents/src/main.go",
		Param{Key: "owner", Value: "golang"},
		Param{Key: "filepath", Value: "/src/main.go"},
	)
}

func TestMethodsAreIndependentTrees(t *testing.T) {
	r := new(Router)
	r.insert("GET", "/users/:id", fakeHandlers("get"))
	r.insert("POST", "/users", fakeHandlers("post"))

	if h, _, _ := r.Lookup("GET", "/users/1", nil); h == nil {
		t.Error("GET /users/1: no match")
	}
	// The GET route must not leak into the POST tree.
	if h, _, _ := r.Lookup("POST", "/users/1", nil); h != nil {
		t.Error("POST /users/1: matched, want no match")
	}
	if h, _, _ := r.Lookup("DELETE", "/users", nil); h != nil {
		t.Error("DELETE /users: matched, but no DELETE tree exists")
	}
}

func TestMaxParams(t *testing.T) {
	r := new(Router)
	r.insert("GET", "/users/:id", fakeHandlers("a"))
	r.insert("GET", "/repos/:owner/:repo/issues/:number", fakeHandlers("b"))
	r.insert("GET", "/src/*filepath", fakeHandlers("c"))

	if got := r.MaxParams(); got != 3 {
		t.Errorf("MaxParams() = %d, want 3", got)
	}
}

func TestTrailingSlashRedirect(t *testing.T) {
	n := newTree(t,
		"/hi",
		"/b/",
		"/search/:query",
		"/cmd/:tool/",
		"/src/*filepath",
		"/x/y",
	)

	// Registered with no slash, requested with one, and the reverse.
	requireNoRoute(t, n, "/hi/", true)
	requireNoRoute(t, n, "/b", true)
	requireNoRoute(t, n, "/x/y/", true)

	// Param routes: "/cmd/:tool/" is registered with a slash.
	requireRoute(t, n, "/cmd/vet/", Param{Key: "tool", Value: "vet"})
	requireNoRoute(t, n, "/cmd/vet", true)

	// "/search/:query" is registered without one.
	requireRoute(t, n, "/search/gopher", Param{Key: "query", Value: "gopher"})
	requireNoRoute(t, n, "/search/gopher/", true)

	// A catch-all needs its slash to bind an empty remainder.
	requireNoRoute(t, n, "/src", true)

	// No redirect can rescue a path that is simply not registered.
	requireNoRoute(t, n, "/nope", false)
	requireNoRoute(t, n, "/nope/", false)
}

// Static and wildcard segments cannot compete at the same position: the
// wildcard would shadow the static sibling at match time, so registration
// fails loudly, in either order.
func TestConflictStaticVersusParam(t *testing.T) {
	requirePanic(t, "conflicts with existing wildcard", func() {
		newTreeNoHelper("/users/:id", "/users/new")
	})
	requirePanic(t, "conflicts with existing children", func() {
		newTreeNoHelper("/users/new", "/users/:id")
	})
}

func TestConflictStaticVersusCatchAll(t *testing.T) {
	requirePanic(t, "conflicts with existing wildcard", func() {
		newTreeNoHelper("/src/*filepath", "/src/index.html")
	})
	requirePanic(t, "conflicts with existing children", func() {
		newTreeNoHelper("/src/index.html", "/src/*filepath")
	})
}

func TestConflictDifferentParamNames(t *testing.T) {
	requirePanic(t, "conflicts with existing wildcard", func() {
		newTreeNoHelper("/users/:id", "/users/:name")
	})
	requirePanic(t, "conflicts with existing wildcard", func() {
		newTreeNoHelper("/users/:id/posts", "/users/:uid/comments")
	})
}

func TestConflictDuplicateRoute(t *testing.T) {
	requirePanic(t, "handlers are already registered", func() {
		newTreeNoHelper("/users/list", "/users/list")
	})
	requirePanic(t, "handlers are already registered", func() {
		newTreeNoHelper("/users/:id", "/users/:id")
	})
	requirePanic(t, "handlers are already registered", func() {
		newTreeNoHelper("/", "/")
	})
}

// The same param name may repeat down a path as long as each position agrees.
func TestNoConflictOnSharedParamPrefix(t *testing.T) {
	n := newTree(t, "/users/:id", "/users/:id/posts", "/users/:id/comments")

	requireRoute(t, n, "/users/9", Param{Key: "id", Value: "9"})
	requireRoute(t, n, "/users/9/posts", Param{Key: "id", Value: "9"})
	requireRoute(t, n, "/users/9/comments", Param{Key: "id", Value: "9"})
	requireNoRoute(t, n, "/users/9/likes", false)
}

func TestMalformedRoutes(t *testing.T) {
	requirePanic(t, "must begin with '/'", func() { newTreeNoHelper("users/:id") })
	requirePanic(t, "non-empty name", func() { newTreeNoHelper("/users/:") })
	requirePanic(t, "non-empty name", func() { newTreeNoHelper("/src/*") })
	requirePanic(t, "only one wildcard per path segment", func() { newTreeNoHelper("/users/:id:name") })
	requirePanic(t, "only allowed at the end of the path", func() { newTreeNoHelper("/src/*filepath/more") })

	requirePanic(t, "at least one handler", func() {
		new(Router).insert("GET", "/users", nil)
	})
	requirePanic(t, "method must not be empty", func() {
		new(Router).insert("", "/users", fakeHandlers("x"))
	})
}

// newTreeNoHelper is newTree without *testing.T, for use inside panic probes.
func newTreeNoHelper(paths ...string) {
	r := new(Router)
	for _, p := range paths {
		r.insert("GET", p, fakeHandlers(p))
	}
}

// The pooled-buffer form must reuse the caller's backing array rather than
// allocating a fresh params slice per lookup: this is what makes the match path
// allocation-free once Engine wires in a sync.Pool.
func TestLookupReusesParamBuffer(t *testing.T) {
	r := new(Router)
	r.insert("GET", "/repos/:owner/:repo", fakeHandlers("x"))

	buf := make([]Param, 0, r.MaxParams())
	want := &buf[:1][0]

	for i := 0; i < 3; i++ {
		h, params, _ := r.Lookup("GET", "/repos/golang/go", buf[:0])
		if h == nil {
			t.Fatal("no match")
		}
		if len(params) != 2 {
			t.Fatalf("params = %v, want 2 entries", params)
		}
		if &params[0] != want {
			t.Fatal("Lookup allocated a new params slice instead of reusing the buffer")
		}
	}
}

func TestMatchPathIsAllocationFree(t *testing.T) {
	r := new(Router)
	for _, p := range []string{"/users", "/users/:id/posts/:pid", "/src/*filepath"} {
		r.insert("GET", p, fakeHandlers(p))
	}
	buf := make([]Param, 0, r.MaxParams())

	got := testing.AllocsPerRun(100, func() {
		r.Lookup("GET", "/users", buf[:0])
		r.Lookup("GET", "/users/7/posts/99", buf[:0])
		r.Lookup("GET", "/src/a/b/c.go", buf[:0])
	})
	if got != 0 {
		t.Errorf("allocations per lookup round = %v, want 0", got)
	}
}
