package zarp

import (
	"net/http"
	"net/netip"
	"strings"
	"sync"
)

const (
	default404Body = "404 page not found"
	default405Body = "405 method not allowed"
)

// Engine's whole integration story is that it is an ordinary http.Handler.
// Asserted here so breaking it is a compile error rather than a surprise at
// whatever line first passes an *Engine to net/http.
var _ http.Handler = (*Engine)(nil)

// Engine ties the router, the Context pool and the handler chain together. It
// implements http.Handler, which is the real integration point: users wanting
// timeouts or graceful shutdown build their own http.Server around it.
type Engine struct {
	// RouterGroup is the root group: every route registered directly on the
	// Engine goes through it, so the verb methods exist in one place only.
	RouterGroup

	router

	// pool holds idle Contexts. sync.Pool keeps a per-P free list, so under
	// load a Get is usually a pointer bump with no lock and no allocation.
	pool sync.Pool

	// noRoute and noMethod are the user's fallback handlers; allNoRoute and
	// allNoMethod are those same handlers behind the root group's middleware,
	// resolved at registration so a miss costs no more than a hit.
	noRoute     []HandlerFunc
	noMethod    []HandlerFunc
	allNoRoute  []HandlerFunc
	allNoMethod []HandlerFunc

	// RedirectTrailingSlash acts on the router's tsr hint: /foo/ redirects to
	// /foo when only the latter is registered, and the other way round.
	RedirectTrailingSlash bool

	// HandleMethodNotAllowed answers 405 with an Allow header when a path is
	// registered under other methods. Off by default: it costs a probe of every
	// other tree on each miss.
	HandleMethodNotAllowed bool

	// ForwardedByClientIP lets Context.ClientIP read X-Forwarded-For and
	// X-Real-Ip. It is off by default: those headers are written by whoever
	// connects, so a directly reachable server that believes them lets a client
	// pick the address your logs, rate limits and allowlists will record. Turn
	// it on only when the server is unreachable except through a proxy.
	ForwardedByClientIP bool

	// TrustedProxies lists the peers whose forwarding headers are believed.
	// When it is set, ClientIP ignores those headers unless RemoteAddr falls
	// inside one of the prefixes, and walks X-Forwarded-For from the right,
	// skipping hops that are themselves listed here.
	//
	//	e.ForwardedByClientIP = true
	//	e.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	//
	// Leaving it empty with ForwardedByClientIP on means "believe the headers
	// from anyone", which is only correct when nothing can reach the server
	// except a proxy that overwrites them.
	TrustedProxies []netip.Prefix

	// MaxMultipartMemory caps how much of a multipart body is buffered in
	// memory before spilling to temporary files.
	MaxMultipartMemory int64
}

// New returns an Engine with no middleware attached.
//
// There is deliberately no Default() constructor bundling a logger and
// recovery: core would have to import middleware/, which imports core. Wire
// them in your own main instead.
func New() *Engine {
	e := &Engine{
		RouterGroup:           RouterGroup{basePath: "/", root: true},
		RedirectTrailingSlash: true,
		MaxMultipartMemory:    defaultMultipartMemory,
	}
	e.engine = e
	e.pool.New = func() any { return e.allocateContext() }
	return e
}

// NoRoute sets the handlers run when no route matches. They run behind the root
// group's middleware, so a logger attached with Use still sees 404s.
func (e *Engine) NoRoute(handlers ...HandlerFunc) *Engine {
	e.noRoute = handlers
	e.rebuildFallbacks()
	return e
}

// NoMethod sets the handlers run when a path exists under other methods and
// HandleMethodNotAllowed is on.
func (e *Engine) NoMethod(handlers ...HandlerFunc) *Engine {
	e.noMethod = handlers
	e.rebuildFallbacks()
	return e
}

func (e *Engine) rebuildFallbacks() {
	e.allNoRoute = e.combineHandlers(e.noRoute)
	e.allNoMethod = e.combineHandlers(e.noMethod)
}

// Run starts an http.Server on addr with this Engine as its handler.
//
// It is a convenience for examples and small services and nothing more: it
// leaves every timeout at its zero value, which is not what a public server
// wants. Build your own http.Server — Engine is an http.Handler — as soon as
// read/write timeouts or graceful shutdown matter.
func (e *Engine) Run(addr string) error {
	// #nosec G114 -- the missing timeouts are the documented point of this
	// function: it is the two-line convenience, and the doc comment above sends
	// anyone serving real traffic to their own http.Server.
	return http.ListenAndServe(addr, e)
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

	method, path := c.Request.Method, c.Request.URL.Path

	handlers, fullPath, tsr := e.lookup(method, path, &c.Params)
	if handlers != nil {
		c.handlers = handlers
		c.fullPath = fullPath
		c.Next()
		// Flush a status that was set but never written to.
		c.Writer.WriteHeaderNow()
		return
	}

	// The same path with a trailing slash added or removed is registered.
	// CONNECT targets an authority, not a path, so it is never redirected.
	//
	// Four operands would usually be worth naming, and this is the exception:
	// lifting them into two booleans measured +8.3% on BenchmarkEngineStatic
	// (p=0.000, n=10) — a route that never reaches this branch, so the cost is
	// handleRequest being laid out differently, not the test itself. Readability
	// buys nothing here that the comment above does not.
	if tsr && e.RedirectTrailingSlash && method != http.MethodConnect && path != "/" {
		e.redirectTrailingSlash(c)
		return
	}

	if e.HandleMethodNotAllowed {
		if allow := e.allowedMethods(path, method); allow != "" {
			c.Header("Allow", allow)
			c.handlers = e.allNoMethod
			e.serveFallback(c, http.StatusMethodNotAllowed, default405Body)
			return
		}
	}

	c.handlers = e.allNoRoute
	e.serveFallback(c, http.StatusNotFound, default404Body)
}

// serveFallback runs a fallback chain, then supplies a default body only if
// nothing in the chain wrote one and nothing changed the status. A custom
// NoRoute handler therefore replaces the default rather than appending to it.
func (e *Engine) serveFallback(c *Context, code int, body string) {
	c.Status(code)
	c.Next()

	if c.Writer.Written() {
		return
	}
	if c.Writer.Status() == code {
		c.writeContentType("text/plain; charset=utf-8")
		_, _ = c.writermem.WriteString(body)
		return
	}
	c.Writer.WriteHeaderNow()
}

// redirectTrailingSlash answers with the path the router says would have
// matched. GET keeps its method on a 301; anything else uses 307, which
// obliges the client to preserve the method and body.
func (e *Engine) redirectTrailingSlash(c *Context) {
	req := c.Request
	path := req.URL.Path

	if len(path) > 1 && path[len(path)-1] == '/' {
		path = path[:len(path)-1]
	} else {
		path += "/"
	}

	code := http.StatusMovedPermanently
	if req.Method != http.MethodGet {
		code = http.StatusTemporaryRedirect
	}

	req.URL.Path = path
	http.Redirect(c.Writer, req, req.URL.String(), code)
	c.Writer.WriteHeaderNow()
}

// allowedMethods returns the comma-separated methods, other than except, that
// have a handler for path — the value of the Allow header on a 405. It walks
// every other tree, which is why HandleMethodNotAllowed is off by default.
func (e *Engine) allowedMethods(path, except string) string {
	var b strings.Builder
	for i := range e.trees {
		if e.trees[i].method == except {
			continue
		}
		if handlers, _, _ := e.trees[i].root.getValue(path, nil); handlers != nil {
			if b.Len() > 0 {
				b.WriteString(", ")
			}
			b.WriteString(e.trees[i].method)
		}
	}
	return b.String()
}
