package render

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/gozarp/zarp"
)

const jsonContentType = "application/json; charset=utf-8"

// JSONRender writes obj as JSON.
type JSONRender struct{ Data any }

// WriteContentType sets application/json unless the handler chose otherwise.
func (r JSONRender) WriteContentType(w http.ResponseWriter) {
	writeContentType(w, jsonContentType)
}

// Render writes Data as JSON, without the trailing newline Encode appends.
func (r JSONRender) Render(w http.ResponseWriter) error {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(r.Data); err != nil {
		return err
	}
	// Encode appends a newline; drop it so the body is exactly the value.
	_, err := w.Write(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
	return err
}

// IndentedJSONRender writes obj as indented JSON. It is for humans reading a
// response by hand; it costs more bytes and more time than JSONRender.
type IndentedJSONRender struct {
	Data   any
	Prefix string
	Indent string
}

// WriteContentType sets application/json unless the handler chose otherwise.
func (r IndentedJSONRender) WriteContentType(w http.ResponseWriter) {
	writeContentType(w, jsonContentType)
}

// Render writes Data as indented JSON, defaulting to four spaces.
func (r IndentedJSONRender) Render(w http.ResponseWriter) error {
	indent := r.Indent
	if indent == "" {
		indent = "    "
	}
	b, err := json.MarshalIndent(r.Data, r.Prefix, indent)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// JSON writes obj as JSON with the given status code.
//
// Context.JSON does the same thing without the interface hop and is what a
// handler should normally use; this exists so JSON composes with the other
// renderers.
func JSON(c *zarp.Context, code int, obj any) error {
	return With(c, code, JSONRender{Data: obj})
}

// IndentedJSON writes obj as indented JSON with the given status code.
func IndentedJSON(c *zarp.Context, code int, obj any) error {
	return With(c, code, IndentedJSONRender{Data: obj})
}
