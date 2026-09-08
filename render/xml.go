package render

import (
	"encoding/xml"
	"net/http"

	"github.com/subhanjanops/gomicro"
)

// XMLRender writes obj as XML.
type XMLRender struct{ Data any }

func (r XMLRender) WriteContentType(w http.ResponseWriter) {
	writeContentType(w, "application/xml; charset=utf-8")
}

func (r XMLRender) Render(w http.ResponseWriter) error {
	return xml.NewEncoder(w).Encode(r.Data)
}

// XML writes obj as XML with the given status code.
func XML(c *gomicro.Context, code int, obj any) error {
	return With(c, code, XMLRender{Data: obj})
}
