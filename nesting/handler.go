package nesting

// HandlerFunc is the request handler signature. Middleware and route handlers
// are the same type: what makes one middleware is that it calls Next.
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

// Handler returns the route's own handler — the last in the chain — or nil.
func (c *Context) Handler() HandlerFunc {
	if len(c.handlers) == 0 {
		return nil
	}
	return c.handlers[len(c.handlers)-1]
}
