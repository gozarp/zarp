package nesting

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------------------------------------------------------------- 404 / NoRoute

func TestDefault404(t *testing.T) {
	e := New()
	e.GET("/exists", func(c *Context) {})

	rec := serve(e, "GET", "/missing")
	if rec.Code != http.StatusNotFound {
		t.Errorf("code = %d, want 404", rec.Code)
	}
	if got := rec.Body.String(); got != default404Body {
		t.Errorf("body = %q, want %q", got, default404Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("content type = %q", ct)
	}
}

func TestNoRouteReplacesTheDefaultBody(t *testing.T) {
	e := New()
	e.NoRoute(func(c *Context) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "no such route"})
	})

	rec := serve(e, "GET", "/missing")
	if rec.Code != http.StatusNotFound {
		t.Errorf("code = %d", rec.Code)
	}
	if got := rec.Body.String(); got != `{"error":"no such route"}` {
		t.Errorf("body = %q", got)
	}
}

func TestNoRouteMayChangeTheStatus(t *testing.T) {
	e := New()
	e.NoRoute(func(c *Context) { c.Status(http.StatusGone) })

	rec := serve(e, "GET", "/missing")
	if rec.Code != http.StatusGone {
		t.Errorf("code = %d, want 410", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty — the default must not be appended", rec.Body.String())
	}
}

func TestNoRouteSeesRootMiddleware(t *testing.T) {
	var ran []string
	e := New()
	e.Use(mark(&ran, "mw"))
	e.NoRoute(func(c *Context) { ran = append(ran, "noroute") })

	serve(e, "GET", "/missing")
	if got := strings.Join(ran, " "); got != "mw noroute" {
		t.Errorf("ran = %q, want the root middleware to see the 404", got)
	}
}

func TestUseAfterNoRouteStillWraps(t *testing.T) {
	// Order of registration must not matter: Use rebuilds the fallback chains.
	var ran []string
	e := New()
	e.NoRoute(func(c *Context) { ran = append(ran, "noroute") })
	e.Use(mark(&ran, "mw"))

	serve(e, "GET", "/missing")
	if got := strings.Join(ran, " "); got != "mw noroute" {
		t.Errorf("ran = %q", got)
	}
}

func TestNoRouteParamsAreEmpty(t *testing.T) {
	e := New()
	e.GET("/user/:id", func(c *Context) {})
	e.NoRoute(func(c *Context) {
		if len(c.Params) != 0 {
			t.Errorf("params on a miss = %v, want none", c.Params)
		}
		if c.FullPath() != "" {
			t.Errorf("fullPath on a miss = %q, want empty", c.FullPath())
		}
	})

	serve(e, "GET", "/nope")
}

// ---------------------------------------------------------------- 405 / NoMethod

func TestMethodNotAllowedOffByDefault(t *testing.T) {
	e := New()
	e.GET("/x", func(c *Context) {})

	rec := serve(e, "POST", "/x")
	if rec.Code != http.StatusNotFound {
		t.Errorf("code = %d, want 404 while HandleMethodNotAllowed is off", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow != "" {
		t.Errorf("Allow = %q, want none", allow)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	e := New()
	e.HandleMethodNotAllowed = true
	e.GET("/x", func(c *Context) {})
	e.PUT("/x", func(c *Context) {})
	e.POST("/other", func(c *Context) {})

	rec := serve(e, "POST", "/x")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("code = %d, want 405", rec.Code)
	}
	if got := rec.Body.String(); got != default405Body {
		t.Errorf("body = %q", got)
	}

	allow := rec.Header().Get("Allow")
	for _, want := range []string{"GET", "PUT"} {
		if !strings.Contains(allow, want) {
			t.Errorf("Allow = %q, want it to list %s", allow, want)
		}
	}
	if strings.Contains(allow, "POST") {
		t.Errorf("Allow = %q, must not list the method that was tried", allow)
	}
}

func TestMethodNotAllowedFallsBackTo404(t *testing.T) {
	e := New()
	e.HandleMethodNotAllowed = true
	e.GET("/x", func(c *Context) {})

	if code := serve(e, "POST", "/unknown").Code; code != http.StatusNotFound {
		t.Errorf("code = %d, want 404 when no method has that path", code)
	}
}

func TestNoMethodHandler(t *testing.T) {
	e := New()
	e.HandleMethodNotAllowed = true
	e.GET("/x", func(c *Context) {})
	e.NoMethod(func(c *Context) {
		c.JSON(http.StatusMethodNotAllowed, map[string]string{"error": "wrong verb"})
	})

	rec := serve(e, "POST", "/x")
	if got := rec.Body.String(); got != `{"error":"wrong verb"}` {
		t.Errorf("body = %q", got)
	}
	if allow := rec.Header().Get("Allow"); allow != "GET" {
		t.Errorf("Allow = %q, want GET", allow)
	}
}

// ---------------------------------------------------------------- trailing slash

func TestRedirectTrailingSlash(t *testing.T) {
	tests := []struct {
		name, registered, request, method, location string
		code                                        int
	}{
		{"add slash", "/dir/", "/dir", "GET", "/dir/", http.StatusMovedPermanently},
		{"drop slash", "/file", "/file/", "GET", "/file", http.StatusMovedPermanently},
		{"post keeps method", "/file", "/file/", "POST", "/file", http.StatusTemporaryRedirect},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := New()
			e.Handle(tc.method, tc.registered, func(c *Context) {})

			rec := serve(e, tc.method, tc.request)
			if rec.Code != tc.code {
				t.Errorf("code = %d, want %d", rec.Code, tc.code)
			}
			if got := rec.Header().Get("Location"); got != tc.location {
				t.Errorf("Location = %q, want %q", got, tc.location)
			}
		})
	}
}

func TestRedirectTrailingSlashPreservesQuery(t *testing.T) {
	e := New()
	e.GET("/dir/", func(c *Context) {})

	rec := serve(e, "GET", "/dir?q=go&page=2")
	if got := rec.Header().Get("Location"); got != "/dir/?q=go&page=2" {
		t.Errorf("Location = %q, want the query preserved", got)
	}
}

func TestRedirectTrailingSlashCanBeDisabled(t *testing.T) {
	e := New()
	e.RedirectTrailingSlash = false
	e.GET("/dir/", func(c *Context) {})

	if code := serve(e, "GET", "/dir").Code; code != http.StatusNotFound {
		t.Errorf("code = %d, want 404 with redirects off", code)
	}
}

func TestRedirectDoesNotFireForRoot(t *testing.T) {
	e := New()
	e.GET("/x", func(c *Context) {})

	rec := serve(e, "GET", "/")
	if rec.Code != http.StatusNotFound {
		t.Errorf("code = %d, want 404 — / must never redirect to //", rec.Code)
	}
}

func TestNoRedirectWhenRouteMatches(t *testing.T) {
	e := New()
	e.GET("/both", func(c *Context) { c.Text(http.StatusOK, "no-slash") })
	e.GET("/both/", func(c *Context) { c.Text(http.StatusOK, "slash") })

	if got := serve(e, "GET", "/both").Body.String(); got != "no-slash" {
		t.Errorf("/both = %q", got)
	}
	if got := serve(e, "GET", "/both/").Body.String(); got != "slash" {
		t.Errorf("/both/ = %q", got)
	}
}

// ---------------------------------------------------------------- Run

func TestRunServesRequests(t *testing.T) {
	e := New()
	e.GET("/ping", func(c *Context) { c.Text(http.StatusOK, "pong") })

	// Run is a thin ListenAndServe; exercise the same wiring through a real
	// server without binding a fixed port.
	srv := httptest.NewServer(e)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/ping")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
	buf := make([]byte, 4)
	n, _ := resp.Body.Read(buf)
	if string(buf[:n]) != "pong" {
		t.Errorf("body = %q", buf[:n])
	}
}

// ---------------------------------------------------------------- benchmarks

func BenchmarkEngine405(b *testing.B) {
	e := New()
	e.HandleMethodNotAllowed = true
	e.GET("/x", func(c *Context) {})
	e.PUT("/x", func(c *Context) {})
	benchServe(b, e, "POST", "/x")
}

func BenchmarkEngineRedirect(b *testing.B) {
	e := New()
	e.GET("/dir/", func(c *Context) {})
	benchServe(b, e, "GET", "/dir")
}
