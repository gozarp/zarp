package render_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/subhanjanops/gomicro"
	"github.com/subhanjanops/gomicro/render"
)

func TestLoadFiles(t *testing.T) {
	dir := t.TempDir()
	page := filepath.Join(dir, "page.html")
	if err := os.WriteFile(page, []byte(`<p>{{.}}</p>`), 0o600); err != nil {
		t.Fatal(err)
	}

	views, err := render.LoadFiles(page)
	if err != nil {
		t.Fatalf("LoadFiles: %v", err)
	}

	rec := serve(func(c *gomicro.Context) {
		views.HTML(c, http.StatusOK, "page.html", "hello")
	})
	if got := rec.Body.String(); got != "<p>hello</p>" {
		t.Errorf("body = %q", got)
	}
}

func TestLoadErrors(t *testing.T) {
	if _, err := render.LoadFiles(filepath.Join(t.TempDir(), "missing.html")); err == nil {
		t.Error("LoadFiles on a missing file: want an error")
	}
	if _, err := render.LoadGlob("[invalid"); err == nil {
		t.Error("LoadGlob on a bad pattern: want an error")
	}
	if _, err := render.LoadGlob(filepath.Join(t.TempDir(), "*.html")); err == nil {
		t.Error("LoadGlob matching nothing: want an error")
	}
}

func TestHTMLWholeTemplateSet(t *testing.T) {
	// An empty Name executes the set itself rather than a named member.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "only.html"), []byte(`solo`), 0o600); err != nil {
		t.Fatal(err)
	}
	views, err := render.LoadGlob(filepath.Join(dir, "*.html"))
	if err != nil {
		t.Fatal(err)
	}

	rec := serve(func(c *gomicro.Context) {
		render.With(c, http.StatusOK, render.HTMLRender{Template: views.Template(), Data: nil})
	})
	if got := rec.Body.String(); got != "solo" {
		t.Errorf("body = %q", got)
	}
}

func TestHTMLUnknownTemplateName(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.html"), []byte(`a`), 0o600); err != nil {
		t.Fatal(err)
	}
	views, err := render.LoadGlob(filepath.Join(dir, "*.html"))
	if err != nil {
		t.Fatal(err)
	}

	var renderErr error
	serve(func(c *gomicro.Context) {
		renderErr = views.HTML(c, http.StatusOK, "nope.html", nil)
	})
	if renderErr == nil {
		t.Error("want an error for a template that is not in the set")
	}
}

func TestRedirectRenderWriteContentTypeIsANoOp(t *testing.T) {
	// http.Redirect writes its own; this exists to satisfy the interface.
	rec := httptest.NewRecorder()
	render.RedirectRender{}.WriteContentType(rec)
	if len(rec.Header()) != 0 {
		t.Errorf("headers = %v, want none", rec.Header())
	}
}

func TestIndentedJSONCustomIndent(t *testing.T) {
	rec := serve(func(c *gomicro.Context) {
		render.With(c, http.StatusOK, render.IndentedJSONRender{
			Data:   map[string]int{"a": 1},
			Prefix: "",
			Indent: "\t",
		})
	})
	if !strings.Contains(rec.Body.String(), "\t\"a\"") {
		t.Errorf("body = %q, want a tab indent", rec.Body)
	}
}

func TestIndentedJSONEncodeError(t *testing.T) {
	var err error
	serve(func(c *gomicro.Context) {
		err = render.IndentedJSON(c, http.StatusOK, make(chan int))
	})
	if err == nil {
		t.Error("want an error encoding a channel")
	}
}

func TestXMLEncodeError(t *testing.T) {
	var err error
	serve(func(c *gomicro.Context) {
		err = render.XML(c, http.StatusOK, make(chan int))
	})
	if err == nil {
		t.Error("want an error encoding a channel as XML")
	}
}

// failingWriter reports an error on every write, so the renderers' error paths
// are exercised rather than assumed.
type failingWriter struct {
	http.ResponseWriter
	err error
}

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestRenderersReportWriteErrors(t *testing.T) {
	boom := errors.New("write failed")

	renderers := map[string]render.Render{
		"json":     render.JSONRender{Data: map[string]int{"a": 1}},
		"indented": render.IndentedJSONRender{Data: map[string]int{"a": 1}},
		"text":     render.TextRender{Format: "hi"},
		"data":     render.DataRender{ContentType: "text/plain", Data: []byte("hi")},
	}
	for name, r := range renderers {
		t.Run(name, func(t *testing.T) {
			w := failingWriter{ResponseWriter: httptest.NewRecorder(), err: boom}
			if err := r.Render(w); !errors.Is(err, boom) {
				t.Errorf("err = %v, want the writer's error", err)
			}
		})
	}
}

func TestTextRenderWithValuesReportsWriteError(t *testing.T) {
	boom := errors.New("write failed")
	w := failingWriter{ResponseWriter: httptest.NewRecorder(), err: boom}

	r := render.TextRender{Format: "%s", Values: []any{"x"}}
	if err := r.Render(w); err == nil {
		t.Error("want the writer's error through fmt.Fprintf")
	}
}
