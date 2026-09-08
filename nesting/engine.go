package nesting

import (
	"net/http"
	"sync"
)

// Engine ties the router, the Context pool and the handler chain together. It
// implements http.Handler, which is the real integration point: users wanting
// timeouts or graceful shutdown build their own http.Server around it.
type Engine struct {
	// RouterGroup is the root group: every route registered directly on the
	// Engine goes through it, so the verb methods exist in one place only.
	RouterGroup

	Router

	// pool holds idle Contexts. sync.Pool keeps a per-P free list, so under
	// load a Get is usually a pointer bump with no lock and no allocation.
	pool sync.Pool

	// noRoute and noMethod are wired up in roadmap steps 2.5 and 2.6.
	noRoute  []HandlerFunc
	noMethod []HandlerFunc

	// RedirectTrailingSlash acts on the router's tsr hint: /foo/ redirects to
	// /foo when only the latter is registered, and the other way round.
	RedirectTrailingSlash bool

	// HandleMethodNotAllowed answers 405 with an Allow header when a path is
	// registered under other methods. Off by default: it costs a probe of every
	// other tree on each miss.
	HandleMethodNotAllowed bool

	// ForwardedByClientIP lets Context.ClientIP trust X-Forwarded-For and
	// X-Real-Ip. Turn it off when the server is exposed directly.
	ForwardedByClientIP bool

	// MaxMultipartMemory caps how much of a multipart body is buffered in
	// memory before spilling to temporary files.
	MaxMultipartMemory int64
}

// Engine satisfies the hooks Context reads. Roadmap step 2.8 replaces the
// interface with a typed field.
var _ engineOptions = (*Engine)(nil)

// Use adds middleware to the root group. It shadows the embedded
// RouterGroup.Use only to return *Engine, so engine-level calls stay chainable.
func (e *Engine) Use(middleware ...HandlerFunc) *Engine {
	e.RouterGroup.Use(middleware...)
	return e
}

func (e *Engine) trustForwardedForHeader() bool { return e.ForwardedByClientIP }
func (e *Engine) maxMultipartMemory() int64     { return e.MaxMultipartMemory }

// New returns an Engine with no middleware attached.
//
// There is deliberately no Default() constructor bundling a logger and
// recovery: core would have to import middleware/, which imports core. Wire
// them in your own main instead.
func New() *Engine {
	e := &Engine{
		RouterGroup:           RouterGroup{basePath: "/", root: true},
		RedirectTrailingSlash: true,
		ForwardedByClientIP:   true,
		MaxMultipartMemory:    defaultMultipartMemory,
	}
	e.RouterGroup.engine = e
	e.pool.New = func() any { return e.allocateContext() }
	return e
}

// allocateContext builds a pooled Context. maxParams is read here, at the
// moment a Context is created, rather than captured when the Engine was built:
// routes are usually registered after New.
func (e *Engine) allocateContext() *Context {
	return &Context{
		Params: make(Params, 0, e.maxParams),
		engine: e,
	}
}

// ServeHTTP implements http.Handler.
func (e *Engine) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	c := e.pool.Get().(*Context)
	c.reset(w, req)

	e.handleRequest(c)

	// Not deferred: a panic recovered inside the chain unwinds within Next, so
	// this is still reached. A panic that escapes the chain entirely leaks one
	// Context, and the pool simply allocates a replacement.
	e.pool.Put(c)
}

func (e *Engine) handleRequest(c *Context) {
	// A Context pooled before a route with more params was registered would
	// otherwise grow its buffer — an allocation — on every request.
	if n := int(e.maxParams); cap(c.Params) < n {
		c.Params = make(Params, 0, n)
	}

	handlers, fullPath, _ := e.Lookup(c.Request.Method, c.Request.URL.Path, &c.Params)
	if handlers != nil {
		c.handlers = handlers
		c.fullPath = fullPath
		c.Next()
		// Flush a status that was set but never written to.
		c.Writer.WriteHeaderNow()
		return
	}

	// Trailing-slash redirects (step 2.7), 405 (2.6) and NoRoute (2.5) land
	// here next; for now a miss is a plain 404.
	e.serveNotFound(c)
}

func (e *Engine) serveNotFound(c *Context) {
	c.Status(http.StatusNotFound)
	c.writeContentType("text/plain; charset=utf-8")
	c.writermem.WriteString("404 page not found")
	c.Writer.WriteHeaderNow()
}
