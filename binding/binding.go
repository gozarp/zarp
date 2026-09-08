// Package binding parses HTTP requests into Go structs.
//
// It is an optional import: zarp's core never depends on it, so a program
// that decodes its own bodies pays nothing for this package — in particular
// nothing for the reflection the form binders use.
//
// That dependency direction is why the entry points are functions taking a
// *zarp.Context rather than methods on it. Core cannot import binding,
// because binding imports core:
//
//	var req CreateUser
//	if err := binding.JSON(c, &req); err != nil {
//		c.AbortWithStatusJSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
//		return
//	}
//
// Bind picks a binder from the request method and Content-Type, which is what
// most handlers want.
package binding

import (
	"net/http"
	"strings"

	"github.com/gozarp/zarp"
)

// Binding parses a request into obj, which must be a non-nil pointer.
type Binding interface {
	// Name identifies the binding in error messages.
	Name() string

	// Bind parses the request into obj.
	Bind(c *zarp.Context, obj any) error
}

// The available bindings. Bind and the shorthand functions below cover the
// common cases; these are exported for handlers that need to choose at runtime.
var (
	JSONBinding      Binding = jsonBinding{}
	QueryBinding     Binding = queryBinding{}
	FormBinding      Binding = formBinding{}
	MultipartBinding Binding = multipartBinding{}
	URIBinding       Binding = uriBinding{}
	HeaderBinding    Binding = headerBinding{}
)

// Content types Default recognises.
const (
	MIMEJSON      = "application/json"
	MIMEPOSTForm  = "application/x-www-form-urlencoded"
	MIMEMultipart = "multipart/form-data"
)

// Default returns the binding implied by an HTTP method and Content-Type.
// Requests without a body bind from the query string.
func Default(method, contentType string) Binding {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodDelete, http.MethodOptions:
		return QueryBinding
	}

	if i := strings.IndexByte(contentType, ';'); i >= 0 {
		contentType = contentType[:i]
	}
	switch strings.TrimSpace(contentType) {
	case MIMEJSON:
		return JSONBinding
	case MIMEMultipart:
		return MultipartBinding
	case MIMEPOSTForm:
		return FormBinding
	default:
		// An unknown body type is more usefully reported by the JSON decoder
		// than guessed at.
		return JSONBinding
	}
}

// Bind parses the request into obj using the binding implied by its method and
// Content-Type, then validates obj.
func Bind(c *zarp.Context, obj any) error {
	return With(c, obj, Default(c.Request.Method, c.ContentType()))
}

// With parses the request into obj using b, then validates obj.
func With(c *zarp.Context, obj any, b Binding) error {
	if err := b.Bind(c, obj); err != nil {
		return err
	}
	return Validate(obj)
}

// JSON parses a JSON body into obj.
func JSON(c *zarp.Context, obj any) error { return With(c, obj, JSONBinding) }

// Query parses the URL query string into obj, using `form` tags.
func Query(c *zarp.Context, obj any) error { return With(c, obj, QueryBinding) }

// Form parses a urlencoded body into obj, using `form` tags.
func Form(c *zarp.Context, obj any) error { return With(c, obj, FormBinding) }

// Multipart parses a multipart body's values into obj, using `form` tags.
// Uploaded files are read with Context.FormFile rather than bound.
func Multipart(c *zarp.Context, obj any) error { return With(c, obj, MultipartBinding) }

// URI parses the matched route parameters into obj, using `uri` tags.
func URI(c *zarp.Context, obj any) error { return With(c, obj, URIBinding) }

// Header parses request headers into obj, using `header` tags.
func Header(c *zarp.Context, obj any) error { return With(c, obj, HeaderBinding) }
