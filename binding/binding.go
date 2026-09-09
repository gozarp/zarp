// Copyright 2026 Subhanjan Adhikary. All rights reserved.
// Use of this source code is governed by a MIT style
// license that can be found in the LICENSE file.

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
//
// The binders themselves are reached through functions — binding.JSONBinding(),
// binding.QueryBinding() and so on — rather than exported variables, so no
// import can reassign one and change how the rest of the program decodes its
// requests. JSONWith builds a binder with settings of your own.
//
// Binding and validation are separate steps. Every function here parses and
// nothing more; BindAndValidate runs the `binding` tag rules afterwards, and
// Validate can be called on its own for a value assembled by hand. Keeping them
// apart means a handler that only wants a decode can have one, and that a
// validation failure is distinguishable from a malformed body.
package binding

import (
	"errors"
	"net/http"
	"strconv"
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

// The package's binders, built once. They are values behind an interface rather
// than exported variables so that no importer can reassign one: a binder swapped
// out at run time would change how every other package in the program decodes
// its requests, and would be a data race if it happened after serving started.
// Reach for JSONWith to configure a binder of your own.
//
// Every binder in this package is also checked against Binding at compile time
// here, so one that stops satisfying the interface fails to build rather than
// failing wherever it is passed to With.
var (
	jsonBinder      Binding = JSONWith(JSONConfig{})
	queryBinder     Binding = queryBinding{}
	formBinder      Binding = formBinding{}
	multipartBinder Binding = multipartBinding{}
	uriBinder       Binding = uriBinding{}
	headerBinder    Binding = headerBinding{}

	_ Binding = unsupportedBinding{}
)

// JSONBinding returns the default JSON binder: a 4 MB body cap, numbers decoded
// normally, unknown fields ignored. JSONWith builds one with other settings.
func JSONBinding() Binding { return jsonBinder }

// QueryBinding returns the binder reading the URL query string.
func QueryBinding() Binding { return queryBinder }

// FormBinding returns the binder reading a urlencoded body.
func FormBinding() Binding { return formBinder }

// MultipartBinding returns the binder reading a multipart body's values.
func MultipartBinding() Binding { return multipartBinder }

// URIBinding returns the binder reading the matched route parameters.
func URIBinding() Binding { return uriBinder }

// HeaderBinding returns the binder reading request headers.
func HeaderBinding() Binding { return headerBinder }

// Content types Default recognises.
const (
	MIMEJSON      = "application/json"
	MIMEPOSTForm  = "application/x-www-form-urlencoded"
	MIMEMultipart = "multipart/form-data"
)

// Default returns the binding implied by an HTTP method and Content-Type.
// Requests without a body bind from the query string.
//
// An unrecognised Content-Type yields a binder that fails with
// UnsupportedMediaTypeError rather than one that guesses. Guessing turns a
// request a server cannot handle into one it appears to handle badly: the
// client gets a JSON syntax error for a body that was never JSON, and the 415
// that would have told it what was wrong never happens.
func Default(method, contentType string) Binding {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodDelete, http.MethodOptions:
		return queryBinder
	}

	if i := strings.IndexByte(contentType, ';'); i >= 0 {
		contentType = contentType[:i]
	}

	// EqualFold, not a switch on the string: RFC 9110 §8.3.1 makes a media
	// type's type and subtype case-insensitive, so "Application/JSON" names the
	// same thing as "application/json" and a client is entitled to send either.
	// Comparing case-insensitively also avoids lowercasing the string, which
	// would allocate on exactly the requests that spell it unusually.
	switch mediaType := strings.TrimSpace(contentType); {
	case strings.EqualFold(mediaType, MIMEJSON):
		return jsonBinder
	case strings.EqualFold(mediaType, MIMEMultipart):
		return multipartBinder
	case strings.EqualFold(mediaType, MIMEPOSTForm):
		return formBinder
	default:
		// The unaltered spelling goes into the error, so the message shows what
		// the client actually sent.
		return unsupportedBinding{contentType: mediaType}
	}
}

// ErrUnsupportedMediaType matches every UnsupportedMediaTypeError, so a handler
// can map one to 415 without naming the type:
//
//	if errors.Is(err, binding.ErrUnsupportedMediaType) {
//		c.AbortWithStatus(http.StatusUnsupportedMediaType)
//		return
//	}
var ErrUnsupportedMediaType = errors.New("binding: unsupported media type")

// UnsupportedMediaTypeError reports a Content-Type no binder handles. It
// carries the offending type, and matches ErrUnsupportedMediaType under
// errors.Is.
type UnsupportedMediaTypeError struct {
	ContentType string
}

func (e *UnsupportedMediaTypeError) Error() string {
	if e.ContentType == "" {
		return "binding: no Content-Type on a request with a body"
	}
	return "binding: unsupported media type " + strconv.Quote(e.ContentType)
}

func (e *UnsupportedMediaTypeError) Unwrap() error { return ErrUnsupportedMediaType }

// unsupportedBinding is what Default returns for a Content-Type it does not
// recognise: a binder that always fails, so the decision surfaces at Bind
// rather than as a nil Binding the caller has to check for.
type unsupportedBinding struct{ contentType string }

func (unsupportedBinding) Name() string { return "unsupported" }

func (b unsupportedBinding) Bind(*zarp.Context, any) error {
	return &UnsupportedMediaTypeError{ContentType: strings.TrimSpace(b.contentType)}
}

// Bind parses the request into obj using the binding implied by its method and
// Content-Type. It does not validate; see BindAndValidate.
func Bind(c *zarp.Context, obj any) error {
	return With(c, obj, Default(c.Request.Method, c.ContentType()))
}

// BindAndValidate parses the request into obj as Bind does, then checks obj
// against its `binding` tags.
//
// A ValidationErrors is worth answering with 422 and a field-by-field body; the
// binding failures before it are 400, 413 or 415. Distinguishing them is the
// reason the two steps are separate:
//
//	var errs binding.ValidationErrors
//	if errors.As(err, &errs) { ... }
func BindAndValidate(c *zarp.Context, obj any) error {
	if err := Bind(c, obj); err != nil {
		return err
	}
	return Validate(obj)
}

// With parses the request into obj using b. It does not validate.
func With(c *zarp.Context, obj any, b Binding) error {
	return b.Bind(c, obj)
}

// JSON parses a JSON body into obj.
func JSON(c *zarp.Context, obj any) error { return With(c, obj, jsonBinder) }

// Query parses the URL query string into obj, using `form` tags.
func Query(c *zarp.Context, obj any) error { return With(c, obj, queryBinder) }

// Form parses a urlencoded body into obj, using `form` tags.
func Form(c *zarp.Context, obj any) error { return With(c, obj, formBinder) }

// Multipart parses a multipart body's values into obj, using `form` tags.
// Uploaded files are read with Context.FormFile rather than bound.
func Multipart(c *zarp.Context, obj any) error { return With(c, obj, multipartBinder) }

// URI parses the matched route parameters into obj, using `uri` tags.
func URI(c *zarp.Context, obj any) error { return With(c, obj, uriBinder) }

// Header parses request headers into obj, using `header` tags.
func Header(c *zarp.Context, obj any) error { return With(c, obj, headerBinder) }
