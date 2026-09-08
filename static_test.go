package zarp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func staticDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"app.css":      "body{}",
		"index.html":   "<h1>home</h1>",
		"favicon.ico":  "icon-bytes",
		"sub/deep.txt": "deep",
	}
	for name, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestStaticServesFiles(t *testing.T) {
	e := New()
	e.Static("/assets", staticDir(t))

	tests := []struct{ path, want string }{
		{"/assets/app.css", "body{}"},
		{"/assets/sub/deep.txt", "deep"},
		{"/assets/", "<h1>home</h1>"}, // directory index
	}
	for _, tc := range tests {
		rec := serve(e, "GET", tc.path)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: code = %d", tc.path, rec.Code)
			continue
		}
		if got := rec.Body.String(); got != tc.want {
			t.Errorf("%s: body = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestStaticSetsContentType(t *testing.T) {
	e := New()
	e.Static("/assets", staticDir(t))

	rec := serve(e, "GET", "/assets/app.css")
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "css") {
		t.Errorf("content type = %q, want css", ct)
	}
}

func TestStaticHEAD(t *testing.T) {
	e := New()
	e.Static("/assets", staticDir(t))

	rec := serve(e, "HEAD", "/assets/app.css")
	if rec.Code != http.StatusOK {
		t.Errorf("code = %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD returned a body: %q", rec.Body)
	}
}

func TestStaticMissFallsToNoRoute(t *testing.T) {
	e := New()
	e.Static("/assets", staticDir(t))
	e.NoRoute(func(c *Context) { c.Text(http.StatusNotFound, "custom 404") })

	rec := serve(e, "GET", "/assets/nope.css")

	if rec.Code != http.StatusNotFound {
		t.Errorf("code = %d", rec.Code)
	}
	if got := rec.Body.String(); got != "custom 404" {
		t.Errorf("body = %q, want the app's own 404 rather than net/http's", got)
	}
}

func TestStaticRejectsTraversal(t *testing.T) {
	dir := staticDir(t)
	secret := filepath.Join(filepath.Dir(dir), "secret.txt")
	if err := os.WriteFile(secret, []byte("do not serve"), 0o600); err != nil {
		t.Fatal(err)
	}

	e := New()
	e.Static("/assets", dir)

	// The router cleans "..", and http.Dir refuses to climb out regardless, so
	// both layers have to fail for this to leak.
	for _, attempt := range []string{
		"/assets/../secret.txt",
		"/assets/..%2Fsecret.txt",
		"/assets/sub/../../secret.txt",
	} {
		rec := serve(e, "GET", attempt)
		if strings.Contains(rec.Body.String(), "do not serve") {
			t.Errorf("%s leaked a file outside the root", attempt)
		}
	}
}

func TestStaticFile(t *testing.T) {
	dir := staticDir(t)
	e := New()
	e.StaticFile("/favicon.ico", filepath.Join(dir, "favicon.ico"))

	rec := serve(e, "GET", "/favicon.ico")
	if rec.Code != http.StatusOK || rec.Body.String() != "icon-bytes" {
		t.Errorf("code=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestStaticInAGroup(t *testing.T) {
	e := New()
	e.Group("/v1").Static("/assets", staticDir(t))

	if got := serve(e, "GET", "/v1/assets/app.css").Body.String(); got != "body{}" {
		t.Errorf("body = %q", got)
	}
}

func TestStaticFSFromCustomFileSystem(t *testing.T) {
	e := New()
	e.StaticFS("/files", http.Dir(staticDir(t)))

	if got := serve(e, "GET", "/files/app.css").Body.String(); got != "body{}" {
		t.Errorf("body = %q", got)
	}
}

func TestStaticRejectsWildcardPath(t *testing.T) {
	for _, bad := range []string{"/assets/:name", "/assets/*filepath"} {
		func() {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("Static(%q) did not panic", bad)
				}
			}()
			New().Static(bad, ".")
		}()
	}
}

func TestStaticCoexistsWithRoutes(t *testing.T) {
	e := New()
	e.GET("/api/ping", func(c *Context) { c.Text(http.StatusOK, "pong") })
	e.Static("/assets", staticDir(t))

	if got := serve(e, "GET", "/api/ping").Body.String(); got != "pong" {
		t.Errorf("api route broken: %q", got)
	}
	if got := serve(e, "GET", "/assets/app.css").Body.String(); got != "body{}" {
		t.Errorf("static broken: %q", got)
	}
}

func BenchmarkStatic(b *testing.B) {
	dir := b.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.css"), []byte("body{}"), 0o600); err != nil {
		b.Fatal(err)
	}
	e := New()
	e.Static("/assets", dir)

	req := httptest.NewRequest(http.MethodGet, "/assets/app.css", nil)
	b.ReportAllocs()
	for b.Loop() {
		e.ServeHTTP(httptest.NewRecorder(), req)
	}
}
