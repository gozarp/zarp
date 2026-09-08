package gomicro

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func chain(hs ...HandlerFunc) []HandlerFunc { return hs }

func serve(e *Engine, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

// ---------------------------------------------------------------- engine

func TestNewDefaults(t *testing.T) {
	e := New()
	if !e.RedirectTrailingSlash {
		t.Error("RedirectTrailingSlash should default to true")
	}
	if !e.ForwardedByClientIP {
		t.Error("ForwardedByClientIP should default to true")
	}
	if e.HandleMethodNotAllowed {
		t.Error("HandleMethodNotAllowed should default to false")
	}
	if e.MaxMultipartMemory != defaultMultipartMemory {
		t.Errorf("MaxMultipartMemory = %d", e.MaxMultipartMemory)
	}
	if _, ok := any(e).(http.Handler); !ok {
		t.Error("Engine must implement http.Handler")
	}
}

func TestServeHTTPHappyPath(t *testing.T) {
	e := New()
	e.addRoute("GET", "/ping", chain(func(c *Context) {
		c.String(http.StatusOK, "pong")
	}))

	rec := serve(e, "GET", "/ping")
	if rec.Code != http.StatusOK || rec.Body.String() != "pong" {
		t.Errorf("code=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestServeHTTPParamsAndFullPath(t *testing.T) {
	e := New()
	e.addRoute("GET", "/user/:id/posts/:pid", chain(func(c *Context) {
		c.String(http.StatusOK, "%s|%s|%s", c.Param("id"), c.Param("pid"), c.FullPath())
	}))

	rec := serve(e, "GET", "/user/7/posts/9")
	if got := rec.Body.String(); got != "7|9|/user/:id/posts/:pid" {
		t.Errorf("body = %q", got)
	}
}

func TestServeHTTPStatusWithoutBody(t *testing.T) {
	e := New()
	e.addRoute("GET", "/created", chain(func(c *Context) {
		c.Status(http.StatusCreated) // never writes a body
	}))

	rec := serve(e, "GET", "/created")
	if rec.Code != http.StatusCreated {
		t.Errorf("code = %d, want 201 — ServeHTTP must flush a deferred status", rec.Code)
	}
}

func TestServeHTTPNotFound(t *testing.T) {
	e := New()
	e.addRoute("GET", "/exists", chain(func(c *Context) {}))

	rec := serve(e, "GET", "/missing")
	if rec.Code != http.StatusNotFound {
		t.Errorf("code = %d, want 404", rec.Code)
	}
	rec = serve(e, "POST", "/exists")
	if rec.Code != http.StatusNotFound {
		t.Errorf("unregistered method: code = %d, want 404", rec.Code)
	}
}

func TestServeHTTPJSON(t *testing.T) {
	e := New()
	e.addRoute("GET", "/j", chain(func(c *Context) {
		c.JSON(http.StatusOK, map[string]string{"message": "pong"})
	}))

	rec := serve(e, "GET", "/j")
	if got := rec.Body.String(); got != `{"message":"pong"}` {
		t.Errorf("body = %q", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("content type = %q", ct)
	}
}

// ---------------------------------------------------------------- chain

func TestChainOrder(t *testing.T) {
	var order []string
	e := New()
	e.addRoute("GET", "/x", chain(
		func(c *Context) {
			order = append(order, "A-before")
			c.Next()
			order = append(order, "A-after")
		},
		func(c *Context) {
			order = append(order, "B-before")
			c.Next()
			order = append(order, "B-after")
		},
		func(c *Context) { order = append(order, "handler") },
	))

	serve(e, "GET", "/x")
	want := "A-before B-before handler B-after A-after"
	if got := strings.Join(order, " "); got != want {
		t.Errorf("order = %q, want %q", got, want)
	}
}

func TestChainWithoutNext(t *testing.T) {
	// A middleware that never calls Next must still let the loop advance.
	var ran []string
	e := New()
	e.addRoute("GET", "/x", chain(
		func(c *Context) { ran = append(ran, "mw") },
		func(c *Context) { ran = append(ran, "handler") },
	))

	serve(e, "GET", "/x")
	if got := strings.Join(ran, " "); got != "mw handler" {
		t.Errorf("ran = %q, want both", got)
	}
}

func TestAbortStopsChain(t *testing.T) {
	var ran []string
	e := New()
	e.addRoute("GET", "/x", chain(
		func(c *Context) {
			ran = append(ran, "outer-before")
			c.Next()
			ran = append(ran, "outer-after")
		},
		func(c *Context) {
			ran = append(ran, "guard")
			c.String(http.StatusUnauthorized, "denied")
			c.Abort()
		},
		func(c *Context) { ran = append(ran, "handler") },
	))

	rec := serve(e, "GET", "/x")
	if got := strings.Join(ran, " "); got != "outer-before guard outer-after" {
		t.Errorf("ran = %q, handler must not run", got)
	}
	if rec.Code != http.StatusUnauthorized || rec.Body.String() != "denied" {
		t.Errorf("code=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestIsAborted(t *testing.T) {
	e := New()
	var beforeAbort, afterAbort bool
	e.addRoute("GET", "/x", chain(func(c *Context) {
		beforeAbort = c.IsAborted()
		c.Abort()
		afterAbort = c.IsAborted()
	}))

	serve(e, "GET", "/x")
	if beforeAbort {
		t.Error("IsAborted true before Abort")
	}
	if !afterAbort {
		t.Error("IsAborted false after Abort")
	}
}

func TestAbortWithStatus(t *testing.T) {
	e := New()
	e.addRoute("GET", "/x", chain(
		func(c *Context) { c.AbortWithStatus(http.StatusForbidden) },
		func(c *Context) { t.Error("handler ran after AbortWithStatus") },
	))

	rec := serve(e, "GET", "/x")
	if rec.Code != http.StatusForbidden || rec.Body.Len() != 0 {
		t.Errorf("code=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestAbortWithStatusJSON(t *testing.T) {
	e := New()
	e.addRoute("GET", "/x", chain(
		func(c *Context) {
			c.AbortWithStatusJSON(http.StatusTeapot, map[string]string{"error": "nope"})
		},
		func(c *Context) { t.Error("handler ran after AbortWithStatusJSON") },
	))

	rec := serve(e, "GET", "/x")
	if rec.Code != http.StatusTeapot || rec.Body.String() != `{"error":"nope"}` {
		t.Errorf("code=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestHandlerReturnsRouteHandler(t *testing.T) {
	e := New()
	marker := "route"
	var seen string
	e.addRoute("GET", "/x", chain(
		func(c *Context) {
			if c.Handler() != nil {
				c.Next()
			}
		},
		func(c *Context) { seen = marker },
	))

	serve(e, "GET", "/x")
	if seen != marker {
		t.Error("route handler did not run")
	}
}

func TestChainLengthCapPanics(t *testing.T) {
	e := New()
	long := make([]HandlerFunc, abortIndex)
	for i := range long {
		long[i] = func(*Context) {}
	}
	defer func() {
		if r := recover(); r == nil {
			t.Error("a chain at the cursor limit did not panic at registration")
		}
	}()
	e.addRoute("GET", "/x", long)
}

// ---------------------------------------------------------------- pooling

func TestPoolDoesNotLeakBetweenRequests(t *testing.T) {
	e := New()
	e.addRoute("GET", "/u/:id", chain(func(c *Context) {
		if _, ok := c.Get("leak"); ok {
			t.Error("key leaked from a previous request")
		}
		if len(c.Params) != 1 {
			t.Errorf("params = %v, want exactly one", c.Params)
		}
		c.Set("leak", true)
		c.String(http.StatusOK, "%s", c.Param("id"))
	}))

	for _, id := range []string{"1", "2", "3"} {
		if got := serve(e, "GET", "/u/"+id).Body.String(); got != id {
			t.Errorf("body = %q, want %q", got, id)
		}
	}
}

func TestPoolUnderConcurrency(t *testing.T) {
	e := New()
	e.addRoute("GET", "/u/:id", chain(func(c *Context) {
		id := c.Param("id")
		c.Set("id", id)
		// Anything read back must belong to this request only.
		if got := c.GetString("id"); got != id {
			t.Errorf("key crossed requests: %q vs %q", got, id)
		}
		c.String(http.StatusOK, "%s", id)
	}))

	var wg sync.WaitGroup
	for i := range 200 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprint(i)
			if got := serve(e, "GET", "/u/"+id).Body.String(); got != id {
				t.Errorf("body = %q, want %q", got, id)
			}
		}(i)
	}
	wg.Wait()
}

func TestParamsBufferGrowsForRoutesRegisteredLate(t *testing.T) {
	e := New()
	e.addRoute("GET", "/a/:x", chain(func(c *Context) { c.String(http.StatusOK, "a") }))
	serve(e, "GET", "/a/1") // pools a Context sized for one param

	var capacity int
	e.addRoute("GET", "/b/:x/:y/:z", chain(func(c *Context) {
		capacity = cap(c.Params)
		c.String(http.StatusOK, "%s%s%s", c.Param("x"), c.Param("y"), c.Param("z"))
	}))

	if got := serve(e, "GET", "/b/1/2/3").Body.String(); got != "123" {
		t.Errorf("body = %q", got)
	}
	if capacity < 3 {
		t.Errorf("params capacity = %d, want at least 3 without growing mid-request", capacity)
	}
}
