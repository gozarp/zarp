package render_test

import (
	"encoding/xml"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gozarp/zarp"
	"github.com/gozarp/zarp/render"
)

// serve runs h as the handler for GET /r and returns what it wrote.
func serve(h zarp.HandlerFunc) *httptest.ResponseRecorder {
	e := zarp.New()
	e.GET("/r", h)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/r", nil))
	return rec
}

type payload struct {
	XMLName xml.Name `json:"-" xml:"user"`
	Name    string   `json:"name" xml:"name"`
	Age     int      `json:"age" xml:"age"`
}

// ---------------------------------------------------------------- JSON

func TestJSON(t *testing.T) {
	rec := serve(func(c *zarp.Context) {
		if err := render.JSON(c, http.StatusCreated, map[string]string{"message": "ok"}); err != nil {
			t.Errorf("render: %v", err)
		}
	})

	if rec.Code != http.StatusCreated {
		t.Errorf("code = %d", rec.Code)
	}
	if got := rec.Body.String(); got != `{"message":"ok"}` {
		t.Errorf("body = %q, want no trailing newline", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("content type = %q", ct)
	}
}

func TestJSONMatchesContextJSON(t *testing.T) {
	// The two paths exist on purpose; they must not disagree about output.
	obj := payload{Name: "octocat", Age: 7}

	viaRender := serve(func(c *zarp.Context) { render.JSON(c, http.StatusOK, obj) })
	viaContext := serve(func(c *zarp.Context) { c.JSON(http.StatusOK, obj) })

	if viaRender.Body.String() != viaContext.Body.String() {
		t.Errorf("render.JSON = %q but c.JSON = %q", viaRender.Body, viaContext.Body)
	}
	if viaRender.Header().Get("Content-Type") != viaContext.Header().Get("Content-Type") {
		t.Error("content types disagree")
	}
}

func TestIndentedJSON(t *testing.T) {
	rec := serve(func(c *zarp.Context) {
		render.IndentedJSON(c, http.StatusOK, payload{Name: "octocat", Age: 7})
	})

	body := rec.Body.String()
	if !strings.Contains(body, "\n") || !strings.Contains(body, `    "name"`) {
		t.Errorf("body = %q, want indented", body)
	}
}

func TestJSONEncodeErrorIsReturned(t *testing.T) {
	var err error
	serve(func(c *zarp.Context) {
		err = render.JSON(c, http.StatusOK, make(chan int))
	})
	if err == nil {
		t.Error("encoding a channel should have failed")
	}
}

// ---------------------------------------------------------------- XML

func TestXML(t *testing.T) {
	rec := serve(func(c *zarp.Context) {
		if err := render.XML(c, http.StatusOK, payload{Name: "octocat", Age: 7}); err != nil {
			t.Errorf("render: %v", err)
		}
	})

	body := rec.Body.String()
	if !strings.Contains(body, "<user>") || !strings.Contains(body, "<name>octocat</name>") {
		t.Errorf("body = %q", body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/xml; charset=utf-8" {
		t.Errorf("content type = %q", ct)
	}
}

// ---------------------------------------------------------------- text / data

func TestText(t *testing.T) {
	rec := serve(func(c *zarp.Context) {
		render.Text(c, http.StatusOK, "100% sure: %s %d")
	})
	if got := rec.Body.String(); got != "100% sure: %s %d" {
		t.Errorf("body = %q, want verbatim", got)
	}
}

func TestString(t *testing.T) {
	rec := serve(func(c *zarp.Context) {
		render.String(c, http.StatusOK, "hello %s, you are %d", "octocat", 7)
	})
	if got := rec.Body.String(); got != "hello octocat, you are 7" {
		t.Errorf("body = %q", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("content type = %q", ct)
	}
}

func TestData(t *testing.T) {
	rec := serve(func(c *zarp.Context) {
		render.Data(c, http.StatusOK, "image/png", []byte{0x89, 'P'})
	})
	if rec.Header().Get("Content-Type") != "image/png" || rec.Body.Len() != 2 {
		t.Errorf("ct = %q len = %d", rec.Header().Get("Content-Type"), rec.Body.Len())
	}
}

func TestHandlerContentTypeWins(t *testing.T) {
	rec := serve(func(c *zarp.Context) {
		c.Header("Content-Type", "application/vnd.custom+json")
		render.JSON(c, http.StatusOK, map[string]int{"a": 1})
	})
	if ct := rec.Header().Get("Content-Type"); ct != "application/vnd.custom+json" {
		t.Errorf("content type = %q, want the handler's choice preserved", ct)
	}
}

// ---------------------------------------------------------------- redirect

func TestRedirect(t *testing.T) {
	rec := serve(func(c *zarp.Context) {
		if err := render.Redirect(c, http.StatusFound, "/elsewhere"); err != nil {
			t.Errorf("render: %v", err)
		}
	})
	if rec.Code != http.StatusFound {
		t.Errorf("code = %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/elsewhere" {
		t.Errorf("Location = %q", got)
	}
}

func TestRedirectRejectsNonRedirectStatus(t *testing.T) {
	var err error
	serve(func(c *zarp.Context) {
		err = render.Redirect(c, http.StatusOK, "/elsewhere")
	})
	if err == nil {
		t.Error("want an error for a non-redirect status")
	}
}

// ---------------------------------------------------------------- HTML

func writeTemplates(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"index.html": `<h1>{{.Title}}</h1><p>{{.Body}}</p>`,
		"bad.html":   `{{.Missing.Field}}`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestHTML(t *testing.T) {
	views, err := render.LoadGlob(filepath.Join(writeTemplates(t), "*.html"))
	if err != nil {
		t.Fatal(err)
	}

	rec := serve(func(c *zarp.Context) {
		data := map[string]string{"Title": "hello", "Body": "world"}
		if err := views.HTML(c, http.StatusOK, "index.html", data); err != nil {
			t.Errorf("render: %v", err)
		}
	})

	if got := rec.Body.String(); got != "<h1>hello</h1><p>world</p>" {
		t.Errorf("body = %q", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("content type = %q", ct)
	}
}

func TestHTMLEscapes(t *testing.T) {
	views, err := render.LoadGlob(filepath.Join(writeTemplates(t), "*.html"))
	if err != nil {
		t.Fatal(err)
	}

	rec := serve(func(c *zarp.Context) {
		data := map[string]string{"Title": "x", "Body": `<script>alert(1)</script>`}
		views.HTML(c, http.StatusOK, "index.html", data)
	})

	if strings.Contains(rec.Body.String(), "<script>") {
		t.Errorf("body = %q — html/template must escape it", rec.Body)
	}
}

func TestHTMLFailedTemplateWritesNothing(t *testing.T) {
	views, err := render.LoadGlob(filepath.Join(writeTemplates(t), "*.html"))
	if err != nil {
		t.Fatal(err)
	}

	var renderErr error
	rec := serve(func(c *zarp.Context) {
		// A struct with no such field is an execution error; nil data is not.
		renderErr = views.HTML(c, http.StatusOK, "bad.html", struct{}{})
	})

	if renderErr == nil {
		t.Fatal("want an execution error")
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want nothing — the buffer exists to avoid half a page", rec.Body)
	}
}

func TestNewTemplatesAndAccessor(t *testing.T) {
	tpl := template.Must(template.New("t").Parse(`hi {{.}}`))
	views := render.NewTemplates(tpl)

	if views.Template() != tpl {
		t.Error("Template() should return the wrapped set")
	}
	rec := serve(func(c *zarp.Context) { views.HTML(c, http.StatusOK, "t", "there") })
	if got := rec.Body.String(); got != "hi there" {
		t.Errorf("body = %q", got)
	}
}

func TestHTMLWithoutTemplates(t *testing.T) {
	var err error
	serve(func(c *zarp.Context) {
		err = render.With(c, http.StatusOK, render.HTMLRender{Name: "x"})
	})
	if err == nil || !strings.Contains(err.Error(), "no templates") {
		t.Errorf("err = %v", err)
	}
}

// ---------------------------------------------------------------- extension

// custom is the reason the Render interface exists: a format the framework
// does not know about.
type custom struct{ lines []string }

func (custom) WriteContentType(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/csv")
}

func (c custom) Render(w http.ResponseWriter) error {
	_, err := w.Write([]byte(strings.Join(c.lines, "\n")))
	return err
}

func TestWithCustomRenderer(t *testing.T) {
	rec := serve(func(c *zarp.Context) {
		if err := render.With(c, http.StatusOK, custom{lines: []string{"a,b", "1,2"}}); err != nil {
			t.Errorf("render: %v", err)
		}
	})

	if got := rec.Body.String(); got != "a,b\n1,2" {
		t.Errorf("body = %q", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/csv" {
		t.Errorf("content type = %q", ct)
	}
}

// ---------------------------------------------------------------- benchmarks

// The pair below is the cost of the interface hop: what the extensible path
// charges over the core's direct writer.
func BenchmarkRenderJSON(b *testing.B) {
	obj := payload{Name: "octocat", Age: 7}
	e := zarp.New()
	e.GET("/r", func(c *zarp.Context) { render.JSON(c, http.StatusOK, obj) })
	req := httptest.NewRequest(http.MethodGet, "/r", nil)
	rec := httptest.NewRecorder()
	b.ReportAllocs()
	for b.Loop() {
		rec.Body.Reset()
		e.ServeHTTP(rec, req)
	}
}

func BenchmarkContextJSON(b *testing.B) {
	obj := payload{Name: "octocat", Age: 7}
	e := zarp.New()
	e.GET("/r", func(c *zarp.Context) { c.JSON(http.StatusOK, obj) })
	req := httptest.NewRequest(http.MethodGet, "/r", nil)
	rec := httptest.NewRecorder()
	b.ReportAllocs()
	for b.Loop() {
		rec.Body.Reset()
		e.ServeHTTP(rec, req)
	}
}
