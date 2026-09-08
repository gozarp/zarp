package zarp

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// The pool is the one place where a mistake is invisible in single-threaded
// tests and catastrophic in production: a field reset forgets to clear leaks
// one request's data into another's. These tests exist to be run under -race.
//
//	docker run --rm -v "$PWD":/src -w /src golang:1.25 go test -race ./...

// TestPoolIsolationUnderLoad drives every part of Context that carries
// per-request state and asserts each request only ever sees its own.
func TestPoolIsolationUnderLoad(t *testing.T) {
	e := New()
	e.Use(func(c *Context) {
		c.Set("mw", c.Param("id"))
		c.Next()
	})
	e.GET("/u/:id/p/:pid", func(c *Context) {
		id, pid := c.Param("id"), c.Param("pid")

		if len(c.Params) != 2 {
			t.Errorf("params = %v, want exactly two", c.Params)
		}
		if got := c.GetString("mw"); got != id {
			t.Errorf("key from middleware = %q, want %q", got, id)
		}
		if got := c.Query("q"); got != id {
			t.Errorf("query = %q, want %q", got, id)
		}
		if got := c.FullPath(); got != "/u/:id/p/:pid" {
			t.Errorf("fullPath = %q", got)
		}
		if c.IsAborted() {
			t.Error("a fresh request arrived already aborted")
		}

		c.Set("handler", pid)
		c.Header("X-Id", id)
		c.Text(http.StatusOK, id+"/"+pid+"/"+c.GetString("handler"))
	})

	const requests = 300
	var wg sync.WaitGroup
	for i := range requests {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id, pid := fmt.Sprint(i), fmt.Sprint(i*2)
			target := "/u/" + id + "/p/" + pid + "?q=" + id

			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))

			want := id + "/" + pid + "/" + pid
			if got := rec.Body.String(); got != want {
				t.Errorf("body = %q, want %q", got, want)
			}
			if got := rec.Header().Get("X-Id"); got != id {
				t.Errorf("X-Id = %q, want %q", got, id)
			}
		}(i)
	}
	wg.Wait()
}

// TestPoolAcrossMixedRoutes interleaves routes of different shapes, so a
// Context is reused for a request whose param count and body differ from the
// one before it.
func TestPoolAcrossMixedRoutes(t *testing.T) {
	e := New()
	e.GET("/none", func(c *Context) { c.Text(http.StatusOK, "none") })
	e.GET("/one/:a", func(c *Context) { c.Text(http.StatusOK, c.Param("a")) })
	e.GET("/three/:a/:b/:c", func(c *Context) {
		c.Text(http.StatusOK, c.Param("a")+c.Param("b")+c.Param("c"))
	})
	e.NoRoute(func(c *Context) { c.Text(http.StatusNotFound, "miss") })

	cases := []struct{ target, want string }{
		{"/none", "none"},
		{"/one/x", "x"},
		{"/three/a/b/c", "abc"},
		{"/nope", "miss"},
	}

	var wg sync.WaitGroup
	for range 100 {
		for _, tc := range cases {
			wg.Add(1)
			go func(target, want string) {
				defer wg.Done()
				rec := httptest.NewRecorder()
				e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
				if got := rec.Body.String(); got != want {
					t.Errorf("%s = %q, want %q", target, got, want)
				}
			}(tc.target, tc.want)
		}
	}
	wg.Wait()
}

// TestCopySurvivesTheOriginalReturningToThePool is the documented reason Copy
// exists: a goroutine outliving the handler must not read pooled memory.
func TestCopySurvivesTheOriginalReturningToThePool(t *testing.T) {
	e := New()
	done := make(chan struct{}, 50)

	e.GET("/u/:id", func(c *Context) {
		c.Set("id", c.Param("id"))
		cp := c.Copy()
		go func() {
			// The original is back in the pool and serving someone else by now.
			if got := cp.Param("id"); got != cp.GetString("id") {
				t.Errorf("copy saw %q but key %q", got, cp.GetString("id"))
			}
			done <- struct{}{}
		}()
		c.Text(http.StatusOK, "ok")
	})

	for i := range 50 {
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/u/%d", i), nil))
	}
	for range 50 {
		<-done
	}
}
