// Package render writes typed responses: JSON, XML, HTML, plain text.
//
// It is an optional import. gomicro's core carries its own Context.JSON,
// Context.Text and Context.Data, which write directly and allocate nothing per
// request; this package is the extensible path, where a response goes through
// an interface so an application can add its own formats.
//
// That duplication is deliberate. An interface call plus a renderer value is
// exactly the overhead the core exists to avoid, so the core does not route
// through Render — but a framework with only a hardcoded set of formats is a
// dead end, so this exists alongside it.
//
// Like binding, entry points are functions taking a *gomicro.Context: core
// cannot import this package, because this package imports core.
//
//	render.XML(c, http.StatusOK, report)
//	render.With(c, http.StatusOK, myFormat{data})
package render

import (
	"net/http"

	"github.com/subhanjanops/gomicro"
)

// Render writes one response body.
type Render interface {
	// WriteContentType sets the response content type, unless the handler has
	// already chosen one.
	WriteContentType(w http.ResponseWriter)

	// Render writes the body. It is called after the status is set, so a
	// failure part-way through cannot un-send the header — a renderer that can
	// fail should build its output before writing any of it.
	Render(w http.ResponseWriter) error
}

// With writes r with the given status code.
func With(c *gomicro.Context, code int, r Render) error {
	c.Status(code)
	r.WriteContentType(c.Writer)
	return r.Render(c.Writer)
}

// writeContentType sets value unless the handler already set one, so a handler
// can override a renderer's choice.
func writeContentType(w http.ResponseWriter, value string) {
	header := w.Header()
	if len(header["Content-Type"]) == 0 {
		header["Content-Type"] = []string{value}
	}
}
