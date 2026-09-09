package zarp

import (
	"net/http"
	"path"
	"strconv"
)

// RouterGroup registers routes under a shared path prefix and a shared
// middleware chain. Engine embeds one — its root group — so e.GET and
// group.GET are the same code, and *Engine can be passed anywhere a group is.
type RouterGroup struct {
	// Handlers is the middleware every route registered through this group
	// runs before its own handlers.
	Handlers []HandlerFunc

	basePath string
	engine   *Engine
	root     bool
}

// IRoutes is anything routes and middleware can be registered on. *Engine and
// *RouterGroup both satisfy it, so a setup function can take either:
//
//	func registerUserRoutes(r zarp.IRoutes) { r.GET("/users/:id", show) }
//
// The methods keep their concrete *RouterGroup return type rather than
// returning the interface, so chaining does not erase the type.
type IRoutes interface {
	Use(...HandlerFunc) *RouterGroup

	Handle(string, string, ...HandlerFunc) *RouterGroup
	Any(string, ...HandlerFunc) *RouterGroup
	GET(string, ...HandlerFunc) *RouterGroup
	POST(string, ...HandlerFunc) *RouterGroup
	PUT(string, ...HandlerFunc) *RouterGroup
	PATCH(string, ...HandlerFunc) *RouterGroup
	DELETE(string, ...HandlerFunc) *RouterGroup
	HEAD(string, ...HandlerFunc) *RouterGroup
	OPTIONS(string, ...HandlerFunc) *RouterGroup
}

// IRouter is an IRoutes that can also nest.
type IRouter interface {
	IRoutes

	Group(string, ...HandlerFunc) *RouterGroup
	BasePath() string
}

var (
	_ IRouter = (*Engine)(nil)
	_ IRouter = (*RouterGroup)(nil)
)

// anyMethods is what Any registers across.
var anyMethods = [...]string{
	http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch,
	http.MethodHead, http.MethodOptions, http.MethodDelete,
	http.MethodConnect, http.MethodTrace,
}

// Group returns a new group nested under this one: its prefix is this group's
// prefix plus relativePath, and its middleware is this group's plus handlers.
//
// Routes already registered are unaffected — the new group only governs what is
// registered through it.
func (g *RouterGroup) Group(relativePath string, handlers ...HandlerFunc) *RouterGroup {
	return &RouterGroup{
		Handlers: g.combineHandlers(handlers),
		basePath: joinPaths(g.basePath, relativePath),
		engine:   g.engine,
	}
}

// Use adds middleware to this group.
//
// It affects only routes registered afterwards: a route's chain is resolved and
// copied at registration, so calling Use later cannot reach back into it.
//
// On the engine's root group it also refreshes the NoRoute and NoMethod chains,
// which run behind the same middleware. Doing it here rather than in an
// Engine.Use wrapper means a chained e.Use(a).Use(b) rebuilds on both calls.
func (g *RouterGroup) Use(middleware ...HandlerFunc) *RouterGroup {
	g.Handlers = append(g.Handlers, middleware...)
	if g.root && g.engine != nil {
		g.engine.rebuildFallbacks()
	}
	return g
}

// BasePath returns the group's path prefix.
func (g *RouterGroup) BasePath() string {
	return g.basePath
}

// Handle registers handlers for an arbitrary HTTP method.
func (g *RouterGroup) Handle(method, relativePath string, handlers ...HandlerFunc) *RouterGroup {
	if !isValidMethod(method) {
		// Quoted: the usual mistakes here are a stray space or newline, and an
		// unquoted message would not show them.
		panic("zarp: http method " + strconv.Quote(method) + " is not valid")
	}
	return g.handle(method, relativePath, handlers)
}

// GET registers handlers for a GET route, the method for reading a resource.
func (g *RouterGroup) GET(relativePath string, handlers ...HandlerFunc) *RouterGroup {
	return g.handle(http.MethodGet, relativePath, handlers)
}

// POST registers handlers for a POST route, the method for creating one.
func (g *RouterGroup) POST(relativePath string, handlers ...HandlerFunc) *RouterGroup {
	return g.handle(http.MethodPost, relativePath, handlers)
}

// PUT registers handlers for a PUT route, the method for replacing one.
func (g *RouterGroup) PUT(relativePath string, handlers ...HandlerFunc) *RouterGroup {
	return g.handle(http.MethodPut, relativePath, handlers)
}

// PATCH registers handlers for a PATCH route, the method for a partial
// update.
func (g *RouterGroup) PATCH(relativePath string, handlers ...HandlerFunc) *RouterGroup {
	return g.handle(http.MethodPatch, relativePath, handlers)
}

// DELETE registers handlers for a DELETE route.
func (g *RouterGroup) DELETE(relativePath string, handlers ...HandlerFunc) *RouterGroup {
	return g.handle(http.MethodDelete, relativePath, handlers)
}

// HEAD registers handlers for a HEAD route: a GET whose response carries
// headers but no body.
func (g *RouterGroup) HEAD(relativePath string, handlers ...HandlerFunc) *RouterGroup {
	return g.handle(http.MethodHead, relativePath, handlers)
}

// OPTIONS registers handlers for an OPTIONS route. zarp does not answer
// OPTIONS on its own, so a CORS preflight needs either this or the CORS
// middleware.
func (g *RouterGroup) OPTIONS(relativePath string, handlers ...HandlerFunc) *RouterGroup {
	return g.handle(http.MethodOptions, relativePath, handlers)
}

// Any registers the same handlers for every standard HTTP method.
func (g *RouterGroup) Any(relativePath string, handlers ...HandlerFunc) *RouterGroup {
	for _, method := range anyMethods {
		g.handle(method, relativePath, handlers)
	}
	return g
}

// handle resolves the full path and the full chain, then hands both to the
// router. Everything about prefixes and middleware is settled here, at
// registration: a request never walks a group.
func (g *RouterGroup) handle(method, relativePath string, handlers []HandlerFunc) *RouterGroup {
	absolutePath := joinPaths(g.basePath, relativePath)
	g.engine.addRoute(method, absolutePath, g.combineHandlers(handlers))
	return g
}

// combineHandlers returns the group's middleware followed by handlers.
//
// The result is allocated at exactly the size it needs. Appending onto
// g.Handlers instead would let two sibling routes share a backing array, and
// the second registration would silently overwrite the first one's handlers.
func (g *RouterGroup) combineHandlers(handlers []HandlerFunc) []HandlerFunc {
	size := len(g.Handlers) + len(handlers)
	if size >= int(abortIndex) {
		panic("zarp: too many handlers")
	}
	merged := make([]HandlerFunc, size)
	copy(merged, g.Handlers)
	copy(merged[len(g.Handlers):], handlers)
	return merged
}

// joinPaths joins a group prefix and a relative path.
//
// path.Join cleans the result but drops a trailing slash, and "/static/" and
// "/static" are different routes in the tree, so it is put back.
func joinPaths(absolutePath, relativePath string) string {
	if relativePath == "" {
		return absolutePath
	}
	joined := path.Join(absolutePath, relativePath)
	if lastChar(relativePath) == '/' && lastChar(joined) != '/' {
		return joined + "/"
	}
	return joined
}

func lastChar(s string) byte {
	if s == "" {
		return 0
	}
	return s[len(s)-1]
}

// isValidMethod reports whether method is a well-formed HTTP token. Methods are
// case-sensitive and conventionally upper case; the router keys its trees on
// the string as given, so "get" would never match a real request.
// isValidMethod reports whether method is an RFC 9110 token, which is what an
// HTTP method has to be.
//
// Not "A-Z only": several IANA-registered methods carry a hyphen —
// BASELINE-CONTROL and VERSION-CONTROL from WebDAV versioning, and
// M-SEARCH from SSDP — and rejecting them would make routes that real clients
// send unregisterable. Methods are case-sensitive, so a lowercase "get" is a
// legal token that simply never matches a client sending "GET"; use the GET
// method for that.
func isValidMethod(method string) bool {
	if method == "" {
		return false
	}
	for i := range len(method) {
		if !isMethodTokenByte(method[i]) {
			return false
		}
	}
	return true
}

func isMethodTokenByte(c byte) bool {
	switch {
	case c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return false
}
