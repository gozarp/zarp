package zarp

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// multipartBody builds a one-file multipart body and its content type.
func multipartBody(t *testing.T) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("upload", "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	part.Write([]byte("data"))
	w.Close()
	return &body, w.FormDataContentType()
}

// ---------------------------------------------------------------- engine

func TestRunReportsListenError(t *testing.T) {
	// Run is a thin ListenAndServe; a malformed address exercises it without
	// binding a port or leaving a server behind.
	e := New()
	e.GET("/x", func(c *Context) {})

	if err := e.Run("this is not an address"); err == nil {
		t.Error("Run on a malformed address: want an error")
	}
}

func TestHandlerIsNilWithoutAChain(t *testing.T) {
	c := &Context{}
	if got := c.Handler(); got != nil {
		t.Errorf("Handler() = %v, want nil for an empty chain", got)
	}
}

// ---------------------------------------------------------------- context

func TestQueryOnNilRequest(t *testing.T) {
	c := &Context{}
	if got := c.Query("q"); got != "" {
		t.Errorf("Query = %q, want empty", got)
	}
	if got := c.PostForm("q"); got != "" {
		t.Errorf("PostForm = %q, want empty", got)
	}
}

func TestFormFileErrors(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader("not multipart"))
	req.Header.Set("Content-Type", "text/plain")
	c := &Context{}
	c.reset(rec, req)

	if _, err := c.FormFile("file"); err == nil {
		t.Error("FormFile on a non-multipart body: want an error")
	}
}

func TestFormFileMissingField(t *testing.T) {
	body, contentType := multipartBody(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/upload", body)
	req.Header.Set("Content-Type", contentType)
	c := &Context{}
	c.reset(rec, req)

	if _, err := c.FormFile("absent"); err == nil {
		t.Error("FormFile for a field that was not uploaded: want an error")
	}
}

func TestClientIPWithoutAPort(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.9" // no port, as some proxies leave it
	c := &Context{engine: &Engine{}}
	c.reset(rec, req)

	if got := c.ClientIP(); got != "192.0.2.9" {
		t.Errorf("ClientIP = %q, want the address as given", got)
	}
}

func TestMultipartFormOnNonMultipartBody(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("a=b"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c := &Context{}
	c.reset(rec, req)

	if _, err := c.MultipartForm(); err == nil {
		t.Error("MultipartForm on a urlencoded body: want an error")
	}
}

// ---------------------------------------------------------------- group

func TestLastChar(t *testing.T) {
	if got := lastChar(""); got != 0 {
		t.Errorf("lastChar(%q) = %d, want 0", "", got)
	}
	if got := lastChar("abc"); got != 'c' {
		t.Errorf("lastChar = %q", got)
	}
}

// ---------------------------------------------------------------- router

func TestCountParams(t *testing.T) {
	tests := map[string]uint16{
		"/static":           0,
		"/a/:x":             1,
		"/a/:x/b/:y/c/:z":   3,
		"/files/*filepath":  1,
		"/a/:x/files/*rest": 2,
	}
	for path, want := range tests {
		if got := countParams(path); got != want {
			t.Errorf("countParams(%q) = %d, want %d", path, got, want)
		}
	}
}

func TestLookupUnmatchedBranches(t *testing.T) {
	// Each case reaches a different arm of the tree walk that the higher-level
	// tests do not: a param with nothing registered beneath it, a catch-all
	// prefix probed one byte short, and a miss inside a static subtree.
	r := &router{}
	h := []HandlerFunc{func(*Context) {}}
	for _, route := range []string{
		"/user/:id",
		"/user/:id/posts",
		"/files/*filepath",
		"/about",
		"/abc",
	} {
		r.addRoute("GET", route, h)
	}

	tests := []struct {
		path      string
		wantFound bool
		wantTSR   bool
	}{
		{"/user/42/comments", false, false}, // param, then a segment with no node
		{"/user/42/posts/", false, true},    // registered without the slash
		{"/fil", false, false},              // partial prefix of the catch-all branch
		{"/abo", false, false},              // partial prefix of a static node
		{"/abcd", false, false},             // longer than a static node
		{"/about/", false, true},            // registered without the slash
		{"/", false, false},                 // root itself is not registered
	}
	for _, tc := range tests {
		ps := make(Params, 0, 4)
		handlers, _, tsr := r.lookup("GET", tc.path, &ps)
		if (handlers != nil) != tc.wantFound || tsr != tc.wantTSR {
			t.Errorf("%s: found=%v tsr=%v, want %v/%v",
				tc.path, handlers != nil, tsr, tc.wantFound, tc.wantTSR)
		}
	}
}

func TestFindWildcardUnnamedCatchAll(t *testing.T) {
	// findWildcard reports the segment; insertChild is what rejects it.
	if w, i, valid := findWildcard("/src/*"); w != "*" || i != 5 || valid {
		t.Errorf("findWildcard = %q, %d, %v", w, i, valid)
	}
}

// ---------------------------------------------------------------- static

func TestStaticFileRejectsWildcardPath(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("StaticFile with a wildcard path did not panic")
		}
	}()
	New().StaticFile("/icons/:name", "./favicon.ico")
}

func TestStaticFileHEAD(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/f.txt"
	if err := os.WriteFile(path, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}

	e := New()
	e.StaticFile("/f.txt", path)

	rec := serve(e, "HEAD", "/f.txt")
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Errorf("code=%d body=%q", rec.Code, rec.Body.String())
	}
}
