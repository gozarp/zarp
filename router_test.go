package zarp

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func hn(name string) []HandlerFunc {
	return []HandlerFunc{func(c *Context) { _ = name }}
}

func mustPanic(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Errorf("no panic; want one mentioning %q", want)
			return
		}
		msg := fmt.Sprint(r)
		if !strings.Contains(msg, want) {
			t.Errorf("panic = %q, want it to mention %q", msg, want)
		}
	}()
	fn()
}

// checkTree asserts the invariants getValue relies on.
func checkTree(t *testing.T, n *node) {
	t.Helper()
	if n.wildChild {
		if len(n.children) != 1 {
			t.Errorf("node %q: wildChild with %d children, want 1", n.path, len(n.children))
		}
		if n.indices != "" {
			t.Errorf("node %q: wildChild with indices %q, want empty", n.path, n.indices)
		}
	} else if n.nType == param || n.nType == catchAll {
		// getValue descends a param/catchAll node via children[0] and never
		// consults indices, so it carries at most one child and no indices.
		if len(n.children) > 1 {
			t.Errorf("node %q: %s node with %d children, want <=1", n.path, "wildcard", len(n.children))
		}
	} else if len(n.indices) != len(n.children) {
		t.Errorf("node %q: %d indices %q but %d children", n.path, len(n.indices), n.indices, len(n.children))
	} else {
		for i, c := range n.children {
			if len(c.path) > 0 && n.indices[i] != c.path[0] {
				t.Errorf("node %q: indices[%d]=%q but child %q", n.path, i, n.indices[i], c.path)
			}
		}
	}
	if n.nType == catchAll && n.wildChild && len(n.children) == 0 {
		t.Errorf("catchAll intermediate %q has no leaf", n.path)
	}
	for _, c := range n.children {
		checkTree(t, c)
	}
}

func newRouter(t *testing.T, routes ...string) *router {
	t.Helper()
	r := &router{}
	for _, p := range routes {
		r.addRoute("GET", p, hn(p))
	}
	for i := range r.trees {
		checkTree(t, r.trees[i].root)
	}
	return r
}

func TestAddRouteLookup(t *testing.T) {
	r := newRouter(t,
		"/", "/user/list", "/user/new", "/src/*filepath",
		"/u/:uid", "/u/:uid/posts", "/u/:uid/posts/:pid", "/about/",
	)
	if len(r.trees) != 1 {
		t.Fatalf("trees = %d, want 1", len(r.trees))
	}
	tests := []struct {
		path       string
		found, tsr bool
		params     []Param
	}{
		{"/", true, false, nil},
		{"/user/list", true, false, nil},
		{"/user/new", true, false, nil},
		{"/about/", true, false, nil},
		{"/about", false, true, nil},
		{"/src/a/b.go", true, false, []Param{{"filepath", "/a/b.go"}}},
		{"/src/", true, false, []Param{{"filepath", "/"}}},
		{"/src", false, true, nil},
		{"/u/7", true, false, []Param{{"uid", "7"}}},
		{"/u/7/posts", true, false, []Param{{"uid", "7"}}},
		{"/u/7/posts/9", true, false, []Param{{"uid", "7"}, {"pid", "9"}}},
		{"/user/", false, false, nil},
		{"/nope", false, false, nil},
		{"/u/7/x", false, false, []Param{{"uid", "7"}}},
	}
	for _, tc := range tests {
		ps := make(Params, 0, r.maxParams)
		hs, _, tsr := r.lookup("GET", tc.path, &ps)
		if (hs != nil) != tc.found || tsr != tc.tsr {
			t.Errorf("%s: found=%v tsr=%v, want %v/%v", tc.path, hs != nil, tsr, tc.found, tc.tsr)
		}
		if len(ps) != len(tc.params) {
			t.Errorf("%s: params %v, want %v", tc.path, ps, tc.params)
			continue
		}
		for i := range ps {
			if ps[i] != tc.params[i] {
				t.Errorf("%s: param %d = %v, want %v", tc.path, i, ps[i], tc.params[i])
			}
		}
	}
}

func TestAddRouteSplitOrders(t *testing.T) {
	for _, order := range [][]string{
		{"/abc", "/ab", "/a"},
		{"/a", "/ab", "/abc"},
		{"/ab", "/abc", "/a"},
	} {
		r := newRouter(t, order...)
		for _, p := range order {
			if hs, _, _ := r.lookup("GET", p, nil); hs == nil {
				t.Errorf("order %v: %s not found", order, p)
			}
		}
	}
}

func TestAddRouteMethods(t *testing.T) {
	r := &router{}
	r.addRoute("GET", "/x", hn("get"))
	r.addRoute("POST", "/x", hn("post"))
	r.addRoute("GET", "/y", hn("get2"))
	if len(r.trees) != 2 {
		t.Errorf("trees = %d, want 2", len(r.trees))
	}
	if hs, _, _ := r.lookup("GET", "/y", nil); hs == nil {
		t.Error("GET /y not found")
	}
	if hs, _, _ := r.lookup("DELETE", "/x", nil); hs != nil {
		t.Error("DELETE /x should not match")
	}
}

func TestAddRouteMaxParams(t *testing.T) {
	r := &router{}
	r.addRoute("GET", "/a/:x", hn("a"))
	if r.maxParams != 1 {
		t.Errorf("maxParams = %d, want 1", r.maxParams)
	}
	r.addRoute("GET", "/b/:x/:y/:z", hn("b"))
	if r.maxParams != 3 {
		t.Errorf("maxParams = %d, want 3", r.maxParams)
	}
	r.addRoute("GET", "/c", hn("c"))
	if r.maxParams != 3 {
		t.Errorf("maxParams = %d, want 3 (must not drop)", r.maxParams)
	}
	r.addRoute("GET", "/d/*fp", hn("d"))
	if r.maxParams != 3 {
		t.Errorf("maxParams = %d, want 3", r.maxParams)
	}
}

func TestAddRoutePanics(t *testing.T) {
	mustPanic(t, "must not be empty", func() { (&router{}).addRoute("", "/x", hn("x")) })
	mustPanic(t, "must begin with", func() { (&router{}).addRoute("GET", "x", hn("x")) })
	mustPanic(t, "at least one handler", func() { (&router{}).addRoute("GET", "/x", nil) })
	mustPanic(t, "already registered", func() {
		r := &router{}
		r.addRoute("GET", "/x", hn("x"))
		r.addRoute("GET", "/x", hn("x2"))
	})
	mustPanic(t, "conflicts", func() { // param then static
		r := &router{}
		r.addRoute("GET", "/user/:id", hn("id"))
		r.addRoute("GET", "/user/new", hn("new"))
	})
	mustPanic(t, "conflicts", func() { // static then param
		r := &router{}
		r.addRoute("GET", "/user/new", hn("new"))
		r.addRoute("GET", "/user/:id", hn("id"))
	})
	mustPanic(t, "conflicts", func() { // different wildcard names
		r := &router{}
		r.addRoute("GET", "/user/:id", hn("id"))
		r.addRoute("GET", "/user/:name", hn("name"))
	})
	mustPanic(t, "conflicts", func() { // route after a catch-all
		r := &router{}
		r.addRoute("GET", "/src/*fp", hn("fp"))
		r.addRoute("GET", "/src/x", hn("x"))
	})
	mustPanic(t, "catch-all", func() { (&router{}).addRoute("GET", "/src/*fp/x", hn("x")) })
	mustPanic(t, "wildcard", func() { (&router{}).addRoute("GET", "/user/:", hn("x")) })
}

func TestAddRoutePriorityOrdering(t *testing.T) {
	r := &router{}
	r.addRoute("GET", "/zebra", hn("z"))
	for i := 0; i < 10; i++ {
		r.addRoute("GET", fmt.Sprintf("/api/v%d", i), hn("api"))
	}
	root := r.trees[0].root
	checkTree(t, root)
	if root.indices[0] != 'a' {
		t.Errorf("indices = %q, want the hot branch first", root.indices)
	}
	for _, p := range []string{"/zebra", "/api/v0", "/api/v9"} {
		if hs, _, _ := r.lookup("GET", p, nil); hs == nil {
			t.Errorf("%s not found after reordering", p)
		}
	}
}

func TestAddRouteBulk(t *testing.T) {
	routes := []string{
		"/", "/repos/:owner/:repo", "/repos/:owner/:repo/issues",
		"/repos/:owner/:repo/issues/:number", "/repos/:owner/:repo/issues/:number/comments",
		"/repos/:owner/:repo/pulls", "/repos/:owner/:repo/pulls/:number",
		"/repos/:owner/:repo/contents/*path", "/users/:user", "/users/:user/repos",
		"/users/:user/followers", "/orgs/:org", "/orgs/:org/members",
		"/gists", "/gists/:id", "/gists/:id/star", "/notifications", "/emojis",
		"/events", "/feeds", "/search/code", "/search/issues", "/search/users",
	}
	r := newRouter(t, routes...)
	if r.maxParams != 3 {
		t.Errorf("maxParams = %d, want 3", r.maxParams)
	}
	probes := map[string]int{
		"/repos/go/x/issues/12/comments": 3,
		"/repos/go/x/contents/a/b/c.go":  3,
		"/users/bob/repos":               1,
		"/search/issues":                 0,
		"/gists/abc/star":                1,
	}
	for path, want := range probes {
		ps := make(Params, 0, r.maxParams+1)
		hs, _, _ := r.lookup("GET", path, &ps)
		if hs == nil {
			t.Errorf("%s: not found", path)
		}
		if len(ps) != want {
			t.Errorf("%s: %d params (%v), want %d", path, len(ps), ps, want)
		}
	}
}

func BenchmarkLookupBulk(b *testing.B) {
	r := &router{}
	for _, p := range []string{
		"/", "/repos/:owner/:repo", "/repos/:owner/:repo/issues/:number",
		"/repos/:owner/:repo/contents/*path", "/users/:user", "/search/issues",
	} {
		r.addRoute("GET", p, hn(p))
	}
	ps := make(Params, 0, r.maxParams)
	b.ReportAllocs()
	for b.Loop() {
		ps = ps[:0]
		r.lookup("GET", "/repos/golang/go/issues/42", &ps)
	}
}

// TestCatchAllEndToEnd pins the three-node catch-all representation from the
// outside: a registration, a request, and the parameter the handler sees.
//
// The tree stores "/src/*filepath" as a static parent ending before the slash,
// an empty intermediate, and a leaf owning "/*filepath". Reading that shape as
// two nodes rather than three suggests the lookup returns the intermediate and
// panics on n.path[2:]. It does not: the static parent has wildChild false, so
// the walk reaches the intermediate through the indices scan, and only the
// intermediate — which does have wildChild — descends to the leaf.
func TestCatchAllEndToEnd(t *testing.T) {
	tests := []struct{ target, want string }{
		{"/src/", "/"},
		{"/src/a", "/a"},
		{"/src/a/b.go", "/a/b.go"},
		{"/src/deeply/nested/path/file.txt", "/deeply/nested/path/file.txt"},
	}
	for _, tc := range tests {
		t.Run(tc.target, func(t *testing.T) {
			var got string
			seen := false

			e := New()
			e.GET("/src/*filepath", func(c *Context) {
				got, seen = c.Param("filepath"), true
			})

			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.target, nil))

			if !seen {
				t.Fatalf("handler never ran; status %d", rec.Code)
			}
			if got != tc.want {
				t.Errorf("filepath = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCatchAllNodeShape(t *testing.T) {
	e := New()
	e.GET("/src/*filepath", func(*Context) {})

	var root *node
	for _, t := range e.trees {
		if t.method == http.MethodGet {
			root = t.root
		}
	}
	if root == nil {
		t.Fatal("no GET tree")
	}
	if root.path != "/src" || root.indices != "/" {
		t.Fatalf("static parent: path=%q indices=%q, want \"/src\" and \"/\"", root.path, root.indices)
	}
	if root.wildChild {
		t.Error("the static parent must not be the wildcard node, or the walk skips the intermediate")
	}

	mid := root.children[0]
	if mid.path != "" || !mid.wildChild {
		t.Fatalf("intermediate: path=%q wildChild=%v, want empty and true", mid.path, mid.wildChild)
	}
	if mid.handlers != nil {
		t.Error("the intermediate must hold no handlers; the leaf owns them")
	}

	leaf := mid.children[0]
	if leaf.path != "/*filepath" {
		t.Errorf("leaf path = %q, want \"/*filepath\"", leaf.path)
	}
	if leaf.handlers == nil {
		t.Error("leaf holds no handlers")
	}
	if key := leaf.path[2:]; key != "filepath" {
		t.Errorf("param name from leaf.path[2:] = %q, want \"filepath\"", key)
	}
}
