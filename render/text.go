package render

import (
	"fmt"
	"io"
	"net/http"

	"github.com/gozarp/zarp"
)

const plainContentType = "text/plain; charset=utf-8"

// TextRender writes plain text. With no Values, Format is written verbatim and
// nothing reaches fmt.
type TextRender struct {
	Format string
	Values []any
}

// WriteContentType sets text/plain unless the handler chose otherwise.
func (r TextRender) WriteContentType(w http.ResponseWriter) {
	writeContentType(w, plainContentType)
}

// Render writes Format, applying Values through fmt only when there are any.
func (r TextRender) Render(w http.ResponseWriter) error {
	if len(r.Values) == 0 {
		_, err := io.WriteString(w, r.Format)
		return err
	}
	_, err := fmt.Fprintf(w, r.Format, r.Values...)
	return err
}

// DataRender writes bytes under an explicit content type.
type DataRender struct {
	ContentType string
	Data        []byte
}

// WriteContentType sets ContentType unless the handler chose otherwise.
func (r DataRender) WriteContentType(w http.ResponseWriter) {
	writeContentType(w, r.ContentType)
}

// Render writes Data verbatim.
func (r DataRender) Render(w http.ResponseWriter) error {
	_, err := w.Write(r.Data)
	return err
}

// Text writes s verbatim with the given status code. Unlike String, s is data
// rather than a format template.
func Text(c *zarp.Context, code int, s string) error {
	return With(c, code, TextRender{Format: s})
}

// String writes a formatted plain-text response.
func String(c *zarp.Context, code int, format string, values ...any) error {
	return With(c, code, TextRender{Format: format, Values: values})
}

// Data writes raw bytes under an explicit content type.
func Data(c *zarp.Context, code int, contentType string, data []byte) error {
	return With(c, code, DataRender{ContentType: contentType, Data: data})
}
