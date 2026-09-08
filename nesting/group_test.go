package nesting

import (
	"net/http"
	"strings"
	"testing"
)

func mark(order *[]string, name string) HandlerFunc {
	return func(c *Context) {
		*order = append(*order, name)
		c.Next()
	}
}

// ---------------------------------------------------------------- verbs

func TestVerbsRegisterOnTheirOwnTree(t *testing.T) {
	e := New()
	verbs := map[string]func(string, ...HandlerFunc) *RouterGroup{
		http.MethodGet:     e.GET,
		http.MethodPost:    e.POST,
		http.MethodPut:     e.PUT,
		http.MethodPatch:   e.PATCH,
		http.MethodDelete:  e.DELETE,
		http.MethodHead:    e.HEAD,
		http.MethodOptions: e.OPTIONS,
	}
	for method, register := range verbs {
		m := method
		register("/x", func(c *Context) { c.Text(http.StatusOK, m) })
	}

	for method := range verbs {
		if got := serve(e, method, "/x").Body.String(); got != method {
			t.Errorf("%s /x = %q, want %q", method, got, method)
		}
	}
	// A method nobody registered still misses.
	if code := serve(e, http.MethodTrace, "/x").Code; code != http.StatusNotFound {
		t.Errorf("TRACE /x = %d, want 404", code)
	}
}

func TestHandleCustomMethod(t *testing.T) {
	e := New()
	e.Handle("PROPFIND", "/dav", func(c *Context) { c.Text(http.StatusOK, "ok") })

	if got := serve(e, "PROPFIND", "/dav").Body.String(); got != "ok" {
		t.Errorf("body = %q", got)
	}
}

func TestHandleRejectsInvalidMethod(t *testing.T) {
	for _, method := range []string{"", "get", "GET ", "G3T"} {
		func() {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("Handle(%q) did not panic", method)
				}
			}()
			New().Handle(method, "/x", func(*Context) {})
		}()
	}
}

func TestAnyRegistersEveryMethod(t *testing.T) {
	e := New()
	e.Any("/health", func(c *Context) { c.Text(http.StatusOK, "up") })

	for _, method := range anyMethods {
		rec := serve(e, method, "/health")
		if rec.Code != http.StatusOK {
			t.Errorf("%s /health = %d, want 200", method, rec.Code)
		}
	}
}

func TestVerbsAreChainable(t *testing.T) {
	e := New()
	e.GET("/a", func(c *Context) { c.Text(http.StatusOK, "a") }).
		GET("/b", func(c *Context) { c.Text(http.StatusOK, "b") })

	if got := serve(e, "GET", "/b").Body.String(); got != "b" {
		t.Errorf("chained registration lost the second route: %q", got)
	}
}

// ---------------------------------------------------------------- groups

func TestGroupPrefixes(t *testing.T) {
	e := New()
	api := e.Group("/api")
	v1 := api.Group("/v1")
	v1.GET("/users/:id", func(c *Context) {
		c.Text(http.StatusOK, c.FullPath()+" "+c.Param("id"))
	})

	if got := api.BasePath(); got != "/api" {
		t.Errorf("api base path = %q", got)
	}
	if got := v1.BasePath(); got != "/api/v1" {
		t.Errorf("v1 base path = %q", got)
	}
	if got := serve(e, "GET", "/api/v1/users/7").Body.String(); got != "/api/v1/users/:id 7" {
		t.Errorf("body = %q", got)
	}
}

func TestGroupMiddlewareOrderAndCount(t *testing.T) {
	var order []string
	e := New()
	e.Use(mark(&order, "engine"))
	api := e.Group("/api", mark(&order, "api"))
	v1 := api.Group("/v1", mark(&order, "v1"))
	v1.GET("/x", func(c *Context) { order = append(order, "handler") })

	serve(e, "GET", "/api/v1/x")
	want := "engine api v1 handler"
	if got := strings.Join(order, " "); got != want {
		t.Errorf("order = %q, want %q", got, want)
	}
}

func TestGroupMiddlewareDoesNotLeakToSiblings(t *testing.T) {
	var order []string
	e := New()
	admin := e.Group("/admin", mark(&order, "auth"))
	admin.GET("/x", func(c *Context) { order = append(order, "admin-handler") })

	public := e.Group("/public")
	public.GET("/y", func(c *Context) { order = append(order, "public-handler") })

	serve(e, "GET", "/public/y")
	if got := strings.Join(order, " "); got != "public-handler" {
		t.Errorf("order = %q — the sibling group's middleware ran", got)
	}
}

// ---------------------------------------------------------------- Use timing

func TestUseAffectsOnlyLaterRoutes(t *testing.T) {
	var ran []string
	e := New()
	e.GET("/before", func(c *Context) { ran = append(ran, "before-handler") })
	e.Use(mark(&ran, "mw"))
	e.GET("/after", func(c *Context) { ran = append(ran, "after-handler") })

	serve(e, "GET", "/before")
	if got := strings.Join(ran, " "); got != "before-handler" {
		t.Errorf("earlier route picked up middleware: %q", got)
	}

	ran = nil
	serve(e, "GET", "/after")
	if got := strings.Join(ran, " "); got != "mw after-handler" {
		t.Errorf("later route = %q, want the middleware first", got)
	}
}

func TestEngineUseIsChainable(t *testing.T) {
	var ran []string
	e := New()
	e.Use(mark(&ran, "a")).Use(mark(&ran, "b"))
	e.GET("/x", func(c *Context) { ran = append(ran, "handler") })

	serve(e, "GET", "/x")
	if got := strings.Join(ran, " "); got != "a b handler" {
		t.Errorf("order = %q", got)
	}
}

// ---------------------------------------------------------------- combineHandlers

// The trap: appending onto the group's slice lets sibling routes share a
// backing array, and the second registration overwrites the first's handlers.
func TestSiblingRoutesDoNotShareHandlerStorage(t *testing.T) {
	var ran []string
	e := New()
	g := e.Group("/g", mark(&ran, "mw"))
	g.GET("/first", func(c *Context) { ran = append(ran, "first") })
	g.GET("/second", func(c *Context) { ran = append(ran, "second") })

	serve(e, "GET", "/g/first")
	if got := strings.Join(ran, " "); got != "mw first" {
		t.Errorf("first route = %q — its handler was overwritten", got)
	}

	ran = nil
	serve(e, "GET", "/g/second")
	if got := strings.Join(ran, " "); got != "mw second" {
		t.Errorf("second route = %q", got)
	}
}

func TestCombineHandlersAllocatesExactly(t *testing.T) {
	g := &RouterGroup{Handlers: []HandlerFunc{func(*Context) {}, func(*Context) {}}}
	merged := g.combineHandlers([]HandlerFunc{func(*Context) {}})

	if len(merged) != 3 {
		t.Fatalf("len = %d, want 3", len(merged))
	}
	if cap(merged) != 3 {
		t.Errorf("cap = %d, want exactly 3 — spare capacity is what lets siblings collide", cap(merged))
	}
}

func TestCombineHandlersRejectsOverlongChain(t *testing.T) {
	g := &RouterGroup{Handlers: make([]HandlerFunc, abortIndex-1)}
	defer func() {
		if r := recover(); r == nil {
			t.Error("a chain at the cursor limit did not panic")
		}
	}()
	g.combineHandlers([]HandlerFunc{func(*Context) {}})
}

// ---------------------------------------------------------------- joinPaths

func TestJoinPaths(t *testing.T) {
	tests := []struct{ base, rel, want string }{
		{"/", "", "/"},
		{"/", "/", "/"},
		{"/api", "", "/api"},
		{"/api", "/v1", "/api/v1"},
		{"/api/", "/v1", "/api/v1"},
		{"/api", "v1", "/api/v1"},
		{"/", "/users/:id", "/users/:id"},
		{"/api", "/v1/", "/api/v1/"},  // trailing slash preserved
		{"/", "/static/", "/static/"}, // path.Join alone would drop it
		{"/api", "//v1//x", "/api/v1/x"},
		{"/api/v1", "../v2", "/api/v2"},
		{"/api", "/*filepath", "/api/*filepath"},
	}
	for _, tc := range tests {
		if got := joinPaths(tc.base, tc.rel); got != tc.want {
			t.Errorf("joinPaths(%q, %q) = %q, want %q", tc.base, tc.rel, got, tc.want)
		}
	}
}

func TestGroupTrailingSlashRoutesAreDistinct(t *testing.T) {
	e := New()
	e.GET("/static", func(c *Context) { c.Text(http.StatusOK, "no-slash") })
	e.Group("/static").GET("/", func(c *Context) { c.Text(http.StatusOK, "slash") })

	if got := serve(e, "GET", "/static").Body.String(); got != "no-slash" {
		t.Errorf("/static = %q", got)
	}
	if got := serve(e, "GET", "/static/").Body.String(); got != "slash" {
		t.Errorf("/static/ = %q", got)
	}
}

// ---------------------------------------------------------------- benchmarks

func BenchmarkGroupedRoute(b *testing.B) {
	e := New()
	e.Use(func(c *Context) { c.Next() })
	v1 := e.Group("/api/v1", func(c *Context) { c.Next() })
	v1.GET("/users/:id", func(c *Context) { c.Text(http.StatusOK, c.Param("id")) })
	benchServe(b, e, "GET", "/api/v1/users/42")
}

func benchChainOf(n int) *Engine {
	e := New()
	for range n {
		e.Use(func(c *Context) { c.Next() })
	}
	e.GET("/x", func(c *Context) { c.Text(http.StatusOK, "ok") })
	return e
}

func BenchmarkChain0(b *testing.B)  { benchServe(b, benchChainOf(0), "GET", "/x") }
func BenchmarkChain1(b *testing.B)  { benchServe(b, benchChainOf(1), "GET", "/x") }
func BenchmarkChain3(b *testing.B)  { benchServe(b, benchChainOf(3), "GET", "/x") }
func BenchmarkChain10(b *testing.B) { benchServe(b, benchChainOf(10), "GET", "/x") }
