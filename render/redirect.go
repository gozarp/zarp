package render

import (
	"fmt"
	"net/http"

	"github.com/gozarp/zarp"
)

// RedirectRender sends an HTTP redirect. It needs the request because
// http.Redirect resolves a relative location against the current path.
type RedirectRender struct {
	Code     int
	Request  *http.Request
	Location string
}

// WriteContentType does nothing: http.Redirect writes its own.
func (r RedirectRender) WriteContentType(http.ResponseWriter) {}

// Render sends the redirect, or reports a status that is not one.
func (r RedirectRender) Render(w http.ResponseWriter) error {
	// 300-308 and nothing else, matching Context.Redirect. 201 is not a
	// redirect: a created resource is answered with its own body and a Location
	// header beside it, not through a helper whose job is to send no body.
	if r.Code < http.StatusMultipleChoices || r.Code > http.StatusPermanentRedirect {
		return fmt.Errorf("render: cannot redirect with status code %d", r.Code)
	}
	http.Redirect(w, r.Request, r.Location, r.Code)
	return nil
}

// Redirect sends an HTTP redirect to location.
func Redirect(c *zarp.Context, code int, location string) error {
	// The status travels inside http.Redirect, so this renderer is written
	// directly rather than through With, which would set it twice.
	r := RedirectRender{Code: code, Request: c.Request, Location: location}
	if err := r.Render(c.Writer); err != nil {
		return err
	}
	c.Writer.WriteHeaderNow()
	return nil
}
