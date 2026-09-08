package zarp

import (
	"net/http"
	"path"
	"strings"
)

// Static serves the files under root at relativePath.
//
//	r.Static("/assets", "./public")   // /assets/app.css -> ./public/app.css
//
// It is built on a catch-all route, so nothing else may be registered beneath
// the same prefix. http.Dir refuses paths that climb out of root, which is what
// makes this safe to point at a directory; it will still follow a symlink
// inside root, so do not put one there that leaves it.
func (g *RouterGroup) Static(relativePath, root string) *RouterGroup {
	return g.StaticFS(relativePath, http.Dir(root))
}

// StaticFS serves an arbitrary http.FileSystem — an embed.FS through
// http.FS, say — at relativePath.
func (g *RouterGroup) StaticFS(relativePath string, fs http.FileSystem) *RouterGroup {
	if strings.ContainsAny(relativePath, ":*") {
		panic("zarp: wildcards are not allowed in a static path: '" + relativePath + "'")
	}

	handler := g.staticHandler(relativePath, fs)
	pattern := path.Join(relativePath, "/*filepath")

	// HEAD as well as GET: browsers and caches use it, and http.FileServer
	// answers it correctly by writing headers and no body.
	g.GET(pattern, handler)
	g.HEAD(pattern, handler)
	return g
}

// StaticFile serves one file at one route.
//
//	r.StaticFile("/favicon.ico", "./public/favicon.ico")
func (g *RouterGroup) StaticFile(relativePath, filepath string) *RouterGroup {
	if strings.ContainsAny(relativePath, ":*") {
		panic("zarp: wildcards are not allowed in a static path: '" + relativePath + "'")
	}

	handler := func(c *Context) {
		http.ServeFile(c.Writer, c.Request, filepath)
	}
	g.GET(relativePath, handler)
	g.HEAD(relativePath, handler)
	return g
}

// staticHandler strips the mounted prefix before handing over to net/http,
// which owns the hard parts: range requests, conditional requests, content
// type sniffing and index.html.
func (g *RouterGroup) staticHandler(relativePath string, fs http.FileSystem) HandlerFunc {
	absolutePath := joinPaths(g.basePath, relativePath)
	fileServer := http.StripPrefix(absolutePath, http.FileServer(fs))

	return func(c *Context) {
		// Check the file first so a miss falls to NoRoute, the same as any
		// other unknown path, rather than to net/http's own 404 page.
		name := c.Param("filepath")
		f, err := fs.Open(name)
		if err != nil {
			c.Writer.WriteHeader(http.StatusNotFound)
			c.handlers = c.engine.allNoRoute
			c.index = -1
			c.Next()
			return
		}
		f.Close()

		fileServer.ServeHTTP(c.Writer, c.Request)
	}
}
