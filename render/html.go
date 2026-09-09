package render

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"

	"github.com/gozarp/zarp"
)

// HTMLRender executes one template from a set.
type HTMLRender struct {
	Template *template.Template
	Name     string
	Data     any
}

// WriteContentType sets text/html unless the handler chose otherwise.
func (r HTMLRender) WriteContentType(w http.ResponseWriter) {
	writeContentType(w, "text/html; charset=utf-8")
}

// Render executes the template into a buffer, then writes it. A template
// that fails half way through therefore produces an error and no body, rather
// than half a page behind a status that has already gone out.
func (r HTMLRender) Render(w http.ResponseWriter) error {
	if r.Template == nil {
		return fmt.Errorf("render: no templates loaded")
	}

	// Execute into a buffer first. A template that fails half way through has
	// already written part of a page, and the status is long gone — better to
	// find out before any of it reaches the client.
	var (
		buf bytes.Buffer
		err error
	)
	if r.Name == "" {
		err = r.Template.Execute(&buf, r.Data)
	} else {
		err = r.Template.ExecuteTemplate(&buf, r.Name, r.Data)
	}
	if err != nil {
		return err
	}

	_, err = w.Write(buf.Bytes())
	return err
}

// Templates is a loaded set of HTML templates.
//
// It is a value the application holds rather than a field on the Engine: core
// stays free of html/template entirely, which is the point of keeping rendering
// out of it. Store it wherever your handlers can reach it.
//
//	views, err := render.LoadGlob("views/*.html")
//	...
//	r.GET("/", func(c *zarp.Context) {
//		views.HTML(c, http.StatusOK, "index.html", data)
//	})
type Templates struct {
	tpl *template.Template
}

// NewTemplates wraps an already-parsed template set.
func NewTemplates(t *template.Template) *Templates {
	return &Templates{tpl: t}
}

// LoadGlob parses every template matching pattern.
func LoadGlob(pattern string) (*Templates, error) {
	t, err := template.ParseGlob(pattern)
	if err != nil {
		return nil, err
	}
	return &Templates{tpl: t}, nil
}

// LoadFiles parses the named template files.
func LoadFiles(files ...string) (*Templates, error) {
	t, err := template.ParseFiles(files...)
	if err != nil {
		return nil, err
	}
	return &Templates{tpl: t}, nil
}

// Template returns the underlying set, for adding functions or parsing more.
func (t *Templates) Template() *template.Template { return t.tpl }

// HTML executes the named template and writes it with the given status code.
func (t *Templates) HTML(c *zarp.Context, code int, name string, data any) error {
	return With(c, code, HTMLRender{Template: t.tpl, Name: name, Data: data})
}
