package zarp

import "net/http"

// HandlerFunc is the request handler signature. Middleware and route handlers
// are the same type: what makes one middleware is that it calls Next.
//
// # Returning does not stop the chain
//
// The chain is a slice walked by a cursor, so a handler that returns without
// calling Next does not stop anything — the loop simply advances to the next
// handler. That is what lets a route handler be an ordinary HandlerFunc with no
// Next call in it, and it is the one thing about this package worth reading
// twice, because middleware written the other way round is an authorization
// bug:
//
//	// WRONG: the request continues to the route handler.
//	func auth(c *zarp.Context) {
//		if !ok(c) {
//			c.JSON(401, errBody)
//			return
//		}
//		c.Next()
//	}
//
//	// RIGHT: Abort stops every enclosing Next loop.
//	func auth(c *zarp.Context) {
//		if !ok(c) {
//			c.AbortWithStatusJSON(401, errBody)
//			return
//		}
//		c.Next()
//	}
//
// Abort and the AbortWith* helpers are the only things that stop a chain.
// Writing a response does not: nothing prevents a later handler from writing
// more, and the status of a response already on the wire cannot be taken back.
type HandlerFunc func(*Context)

// abortIndex is a cursor value larger than any legal handler chain, so a
// Context whose index reaches it runs no further handlers at any level of the
// stack. addRoute rejects chains that could reach it.
const abortIndex int8 = 63

// Next runs the handlers after the current one.
//
// The chain is a slice walked by an index on the Context, not a stack of nested
// closures: a hop costs one integer increment and no allocation. Middleware that
// wants to act after the rest of the chain calls Next in its middle — the loop
// below resumes when the nested call returns, because the cursor is shared.
//
// The loop is a for, not an if, on purpose: a handler that never calls Next
// still lets the enclosing loop advance to the next one, so route handlers and
// non-calling middleware need no special case.
func (c *Context) Next() {
	c.index++
	for c.index < int8(len(c.handlers)) {
		c.handlers[c.index](c)
		c.index++
	}
}

// IsAborted reports whether the chain was stopped by Abort.
func (c *Context) IsAborted() bool {
	return c.index >= abortIndex
}

// Abort stops the chain: no further handler runs, at any level of the stack,
// because every enclosing Next loop sees a cursor past its own length. It does
// not stop the current handler — return from it yourself.
func (c *Context) Abort() {
	c.index = abortIndex
}

// AbortWithStatus stops the chain and writes the status with an empty body.
func (c *Context) AbortWithStatus(code int) {
	c.Status(code)
	c.Writer.WriteHeaderNow()
	c.Abort()
}

// AbortWithStatusJSON stops the chain and writes obj as the JSON body.
func (c *Context) AbortWithStatusJSON(code int, obj any) {
	c.Abort()
	c.JSON(code, obj)
}

// NotFound abandons the rest of the chain and answers the request with the
// engine's NoRoute handlers, exactly as an unmatched path would be answered.
//
// It is for a handler that matched a route but found nothing behind it — a
// static file that is not there, an id that does not exist — and wants the
// application's own 404 rather than one of its own invention:
//
//	func (s *store) show(c *zarp.Context) {
//		user, ok := s.get(c.Param("id"))
//		if !ok {
//			c.NotFound()
//			return
//		}
//		c.JSON(http.StatusOK, user)
//	}
//
// The status is recorded, not sent, so a NoRoute handler is free to write a
// different one. Return from the calling handler afterwards: like Next, this
// runs handlers rather than unwinding the stack.
func (c *Context) NotFound() {
	// From inside the fallback chain there is nothing further to fall back to,
	// and restarting it would recurse until the stack ran out.
	if c.engine == nil || sameChain(c.handlers, c.engine.allNoRoute) {
		c.Status(http.StatusNotFound)
		c.Writer.WriteHeaderNow()
		c.Abort()
		return
	}

	// Rewind onto the fallback chain and hand it to the same code an unmatched
	// path goes through, so a handler's 404 and the router's own are the same
	// response — including the default body when no NoRoute handler is set.
	c.handlers = c.engine.allNoRoute
	c.index = -1
	c.engine.serveFallback(c, http.StatusNotFound, default404Body)
}

// sameChain reports whether two handler slices share a backing array, which is
// how a Context tells "I am running the fallback chain" from "I am running a
// route's chain" without carrying a flag for it.
func sameChain(a, b []HandlerFunc) bool {
	if len(a) != len(b) {
		return false
	}
	return len(a) == 0 || &a[0] == &b[0]
}

// Handler returns the route's own handler — the last in the chain — or nil.
func (c *Context) Handler() HandlerFunc {
	if len(c.handlers) == 0 {
		return nil
	}
	return c.handlers[len(c.handlers)-1]
}
