package nesting

import (
	"net/http"
	"path"
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
func (g *RouterGroup) Use(middleware ...HandlerFunc) *RouterGroup {
	g.Handlers = append(g.Handlers, middleware...)
	return g
}

// BasePath returns the group's path prefix.
func (g *RouterGroup) BasePath() string {
	return g.basePath
}

// Handle registers handlers for an arbitrary HTTP method.
func (g *RouterGroup) Handle(method, relativePath string, handlers ...HandlerFunc) *RouterGroup {
	if !isValidMethod(method) {
		panic("gomicro: http method " + method + " is not valid")
	}
	return g.handle(method, relativePath, handlers)
}

func (g *RouterGroup) GET(relativePath string, handlers ...HandlerFunc) *RouterGroup {
	return g.handle(http.MethodGet, relativePath, handlers)
}

func (g *RouterGroup) POST(relativePath string, handlers ...HandlerFunc) *RouterGroup {
	return g.handle(http.MethodPost, relativePath, handlers)
}

func (g *RouterGroup) PUT(relativePath string, handlers ...HandlerFunc) *RouterGroup {
	return g.handle(http.MethodPut, relativePath, handlers)
}

func (g *RouterGroup) PATCH(relativePath string, handlers ...HandlerFunc) *RouterGroup {
	return g.handle(http.MethodPatch, relativePath, handlers)
}

func (g *RouterGroup) DELETE(relativePath string, handlers ...HandlerFunc) *RouterGroup {
	return g.handle(http.MethodDelete, relativePath, handlers)
}

func (g *RouterGroup) HEAD(relativePath string, handlers ...HandlerFunc) *RouterGroup {
	return g.handle(http.MethodHead, relativePath, handlers)
}

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
		panic("gomicro: too many handlers")
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
func isValidMethod(method string) bool {
	if method == "" {
		return false
	}
	for i := 0; i < len(method); i++ {
		if c := method[i]; c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}
