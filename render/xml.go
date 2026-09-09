// Copyright 2026 Subhanjan Adhikary. All rights reserved.
// Use of this source code is governed by a MIT style
// license that can be found in the LICENSE file.

package render

import (
	"encoding/xml"
	"net/http"

	"github.com/gozarp/zarp"
)

// XMLRender writes obj as XML.
type XMLRender struct{ Data any }

// WriteContentType sets application/xml unless the handler chose otherwise.
func (r XMLRender) WriteContentType(w http.ResponseWriter) {
	writeContentType(w, "application/xml; charset=utf-8")
}

// Render writes Data as XML.
func (r XMLRender) Render(w http.ResponseWriter) error {
	return xml.NewEncoder(w).Encode(r.Data)
}

// XML writes obj as XML with the given status code.
func XML(c *zarp.Context, code int, obj any) error {
	return With(c, code, XMLRender{Data: obj})
}
