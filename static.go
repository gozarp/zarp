// Copyright 2026 Subhanjan Adhikary. All rights reserved.
// Use of this source code is governed by a MIT style
// license that can be found in the LICENSE file.

package zarp

import (
	"io/fs"
	"net/http"
	"path"
	"path/filepath"
	"strings"
)

// Static serves the files under root at relativePath.
//
//	r.Static("/assets", "./public")   // /assets/app.css -> ./public/app.css
//
// It is built on a catch-all route, so nothing else may be registered beneath
// the same prefix.
//
// The tree is served through http.Dir, which refuses paths that climb out of
// root but will follow a symlink inside it to anywhere on the filesystem. When
// the directory is not entirely under your control — user uploads, a checked-out
// repository, an extracted archive — serve it through SecureDir instead, which
// refuses to leave the root:
//
//	r.StaticFS("/assets", zarp.SecureDir("./public"))
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

// SecureDir is an http.FileSystem serving the tree under root, like http.Dir,
// except that it also refuses any path whose symlinks resolve outside root.
//
//	r.StaticFS("/assets", zarp.SecureDir("./public"))
//
// http.Dir already rejects "../" and absolute paths; what it does not do is
// stop a symlink inside the tree from pointing anywhere on the filesystem.
// r.Static("/assets", "./public") reads like a filesystem boundary, and with
// this it is one.
//
// The root is resolved once, when SecureDir is called. Each Open then resolves
// the requested path and compares the two, which costs a syscall per request —
// the reason this is opt-in rather than the default.
//
// The check is against links already in the tree, which is the realistic case:
// an extracted archive, a repository checkout, a directory of uploads. It is
// not a defence against a local attacker who can create symlinks while requests
// are in flight, because the path is resolved and then opened, and nothing
// short of openat2-style syscalls closes that window.
func SecureDir(root string) http.FileSystem {
	resolved, err := filepath.Abs(root)
	if err == nil {
		// EvalSymlinks so that a root which is itself reached through a link
		// compares equal to the paths opened beneath it.
		resolved, err = filepath.EvalSymlinks(resolved)
	}
	return &secureDir{dir: http.Dir(root), root: resolved, rootErr: err}
}

type secureDir struct {
	dir     http.Dir
	root    string
	rootErr error
}

func (d *secureDir) Open(name string) (http.File, error) {
	if d.rootErr != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: d.rootErr}
	}

	// http.Dir owns the first half of the problem: cleaning the path, rejecting
	// null bytes, and refusing anything that climbs out of the root textually.
	f, err := d.dir.Open(name)
	if err != nil {
		return nil, err
	}

	target := filepath.Join(d.root, filepath.FromSlash(path.Clean("/"+name)))
	target, err = filepath.EvalSymlinks(target)
	if err != nil {
		_ = f.Close()
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	if !within(d.root, target) {
		_ = f.Close()
		// fs.ErrNotExist rather than a distinct error: a 404 tells a prober
		// nothing about what is on the other side of the link.
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return f, nil
}

// within reports whether target is root or sits beneath it.
func within(root, target string) bool {
	if target == root {
		return true
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
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
			c.NotFound()
			return
		}
		// Opened only to learn whether it exists; net/http opens it again to
		// serve it.
		_ = f.Close()

		fileServer.ServeHTTP(c.Writer, c.Request)
	}
}
