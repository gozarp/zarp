package middleware

import (
	"context"
	"time"

	"github.com/gozarp/zarp"
)

// Timeout gives every request below it a deadline.
//
//	r.Use(middleware.Timeout(5 * time.Second))
//
// It puts a deadline on the request's context, which is what database drivers,
// outbound HTTP calls and anything else taking a context already watch, and
// which Context.Deadline, Done and Err expose to the handler:
//
//	select {
//	case result := <-slow(c):
//		c.JSON(http.StatusOK, result)
//	case <-c.Done():
//		c.AbortWithStatus(http.StatusGatewayTimeout)
//	}
//
// What it deliberately does not do is interrupt a handler that ignores the
// context, or write a status on its own. Go cannot stop a running goroutine,
// so a middleware that "times out" a handler is really one that writes a
// response while the handler is still running and may write its own — two
// writers on one connection, and a data race on whatever the handler touches
// after. A deadline the handler honours is the only kind that means anything.
//
// The context is cancelled when the chain returns, so work started under it and
// left running is cancelled rather than leaked.
//
// This costs one allocation per request: Request.WithContext shallow-copies the
// request, which is why it is middleware you opt into rather than engine
// behaviour.
func Timeout(d time.Duration) zarp.HandlerFunc {
	return func(c *zarp.Context) {
		if c.Request == nil {
			c.Next()
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), d)
		defer cancel()

		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
