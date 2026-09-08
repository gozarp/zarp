package binding

import "github.com/subhanjanops/gomicro"

// queryBinding reads the URL query string. It goes through the Context's own
// accessors so the query is parsed once per request, not once per binder.
type queryBinding struct{}

func (queryBinding) Name() string { return "query" }

func (queryBinding) Bind(c *gomicro.Context, obj any) error {
	return mapSource(obj, "form", c.GetQueryArray)
}

// formBinding reads a urlencoded body.
type formBinding struct{}

func (formBinding) Name() string { return "form" }

func (formBinding) Bind(c *gomicro.Context, obj any) error {
	return mapSource(obj, "form", c.GetPostFormArray)
}

// multipartBinding reads the value fields of a multipart body. Uploaded files
// are not bound: read them with Context.FormFile, which hands back the header
// without copying the file into memory.
type multipartBinding struct{}

func (multipartBinding) Name() string { return "multipart" }

func (multipartBinding) Bind(c *gomicro.Context, obj any) error {
	if _, err := c.MultipartForm(); err != nil {
		return err
	}
	return mapSource(obj, "form", c.GetPostFormArray)
}
