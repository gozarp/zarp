package gomicro

import "strings"

// HandlerFunc is the request handler signature. It lives here for now so the
// router compiles standalone; per CLAUDE.md's build order it moves to
// handler.go at step 4 together with chain execution.
type HandlerFunc func(*Context)

// Param is a single URL parameter, a Key/Value pair.
//
// Params are carried as a flat slice rather than a map: a map costs a hash, a
// bucket allocation and GC pressure on every request with a wildcard route.
type Param struct {
	Key   string
	Value string
}

// Params is the set of params matched for one request, in the order the
// wildcards appear in the route path.
type Params []Param

// Get returns the value of the first param matching name, and whether it was
// found. Linear scan: routes have a handful of params at most, so this beats a
// map on every realistic input.
func (ps Params) Get(name string) (string, bool) {
	for i := range ps {
		if ps[i].Key == name {
			return ps[i].Value, true
		}
	}
	return "", false
}

// ByName returns the value of the first param matching name, or "".
func (ps Params) ByName(name string) string {
	v, _ := ps.Get(name)
	return v
}

type nodeType uint8

const (
	static   nodeType = iota // default, a plain path segment
	root                     // tree root
	param                    // ":name"
	catchAll                 // "*name"
)

// node is a radix tree node. A node holds the longest path fragment shared by
// its whole subtree; static children are keyed by their first byte via indices,
// so picking the next child is a byte scan over a short string, not a map
// lookup.
type node struct {
	path      string
	indices   string // first byte of each static child, positionally aligned with children
	wildChild bool   // the (single) child is a ':' or '*' wildcard
	nType     nodeType
	priority  uint32
	children  []*node
	handlers  []HandlerFunc
	fullPath  string // registered route, kept for panic messages and diagnostics
}

// methodTree pairs an HTTP method with its own tree root. A slice scanned
// linearly beats map[string]*node here: there are ~9 methods, and the scan is a
// few pointer compares with no hashing and no allocation.
type methodTree struct {
	method string
	root   *node
}

// Router is the method -> radix tree registry. Engine (build order step 3) will
// embed it; the exported Insert/Lookup pair exists so the out-of-package
// benchmarks in benchmarks/ can exercise the tree directly.
type Router struct {
	trees     []methodTree
	maxParams uint16
}

// Insert registers handlers for method and path. It panics on malformed or
// conflicting routes, at registration time, never at request time.
func (r *Router) Insert(method, path string, handlers []HandlerFunc) {
	r.insert(method, path, handlers)
}

// Lookup matches path in method's tree. params is an optional scratch buffer:
// pass a pooled slice with len 0 to keep matching allocation-free, or nil to
// let the router allocate only when the route actually has params.
func (r *Router) Lookup(method, path string, params []Param) ([]HandlerFunc, []Param, bool) {
	n := r.tree(method)
	if n == nil {
		return nil, nil, false
	}
	return n.getValueInto(path, params)
}

// MaxParams is the largest number of params any registered route can produce.
// Engine sizes its pooled param buffers from this.
func (r *Router) MaxParams() uint16 { return r.maxParams }

func (r *Router) insert(method, path string, handlers []HandlerFunc) {
	if method == "" {
		panic("gomicro: HTTP method must not be empty")
	}
	if len(path) == 0 || path[0] != '/' {
		panic("gomicro: path must begin with '/', got '" + path + "'")
	}
	if len(handlers) == 0 {
		panic("gomicro: at least one handler is required for path '" + path + "'")
	}

	if np := countParams(path); np > r.maxParams {
		r.maxParams = np
	}

	tree := r.tree(method)
	if tree == nil {
		tree = &node{fullPath: "/"}
		r.trees = append(r.trees, methodTree{method: method, root: tree})
	}
	tree.addRoute(path, handlers)
}

func (r *Router) tree(method string) *node {
	for i := range r.trees {
		if r.trees[i].method == method {
			return r.trees[i].root
		}
	}
	return nil
}

func countParams(path string) uint16 {
	var n uint16
	for i := 0; i < len(path); i++ {
		if path[i] == ':' || path[i] == '*' {
			n++
		}
	}
	return n
}

// addRoute adds a route to the tree rooted at n, splitting existing nodes on
// common prefixes as needed. This is setup time: allocating freely here is fine.
func (n *node) addRoute(path string, handlers []HandlerFunc) {
	fullPath := path
	n.priority++

	// Empty tree.
	if len(n.path) == 0 && len(n.children) == 0 {
		n.insertChild(path, fullPath, handlers)
		n.nType = root
		return
	}

	parentFullPathIndex := 0

walk:
	for {
		i := longestCommonPrefix(path, n.path)

		// Split the edge: the current node keeps the shared prefix, everything
		// below it moves into a new child.
		if i < len(n.path) {
			child := node{
				path:      n.path[i:],
				wildChild: n.wildChild,
				nType:     static,
				indices:   n.indices,
				children:  n.children,
				handlers:  n.handlers,
				priority:  n.priority - 1,
				fullPath:  n.fullPath,
			}
			n.children = []*node{&child}
			n.indices = string([]byte{n.path[i]})
			n.path = path[:i]
			n.handlers = nil
			n.wildChild = false
			n.fullPath = fullPath[:parentFullPathIndex+i]
		}

		// Make the new node a child of the current one.
		if i < len(path) {
			path = path[i:]
			c := path[0]

			// A '/' directly after a param: descend into the param's subtree.
			if n.nType == param && c == '/' && len(n.children) == 1 {
				parentFullPathIndex += len(n.path)
				n = n.children[0]
				n.priority++
				continue walk
			}

			// An existing child already starts with this byte.
			for j := 0; j < len(n.indices); j++ {
				if c == n.indices[j] {
					parentFullPathIndex += len(n.path)
					j = n.incrementChildPrio(j)
					n = n.children[j]
					continue walk
				}
			}

			if c != ':' && c != '*' && n.nType != catchAll {
				// A static segment cannot live beside a wildcard: the wildcard
				// would swallow it at match time. Reject it up front.
				if n.wildChild {
					wc := n.children[len(n.children)-1]
					panic("gomicro: static segment '" + path + "' in new path '" + fullPath +
						"' conflicts with existing wildcard '" + wc.path +
						"' in existing path '" + wc.fullPath + "'")
				}
				n.indices += string([]byte{c})
				child := &node{fullPath: fullPath}
				n.children = append(n.children, child)
				n.incrementChildPrio(len(n.indices) - 1)
				n = child
			} else if n.wildChild {
				// Inserting a wildcard where one already exists: it has to be
				// the exact same wildcard, otherwise the routes conflict.
				n = n.children[len(n.children)-1]
				n.priority++

				if len(path) >= len(n.path) && n.path == path[:len(n.path)] &&
					n.nType != catchAll &&
					(len(n.path) >= len(path) || path[len(n.path)] == '/') {
					continue walk
				}

				pathSeg := path
				if n.nType != catchAll {
					pathSeg, _, _ = strings.Cut(pathSeg, "/")
				}
				panic("gomicro: wildcard segment '" + pathSeg + "' in new path '" + fullPath +
					"' conflicts with existing wildcard '" + n.path +
					"' in existing path '" + n.fullPath + "'")
			}

			n.insertChild(path, fullPath, handlers)
			return
		}

		// The path is fully consumed: this node is its leaf.
		if n.handlers != nil {
			panic("gomicro: handlers are already registered for path '" + fullPath + "'")
		}
		n.handlers = handlers
		n.fullPath = fullPath
		return
	}
}

// insertChild builds out the remaining path below n, creating param and
// catch-all nodes as it goes.
func (n *node) insertChild(path, fullPath string, handlers []HandlerFunc) {
	for {
		wildcard, i, valid := findWildcard(path)
		if i < 0 { // no wildcard left
			break
		}
		if !valid {
			panic("gomicro: only one wildcard per path segment is allowed, has: '" +
				wildcard + "' in path '" + fullPath + "'")
		}
		if len(wildcard) < 2 {
			panic("gomicro: wildcards must be named with a non-empty name in path '" + fullPath + "'")
		}
		// A wildcard cannot be added beside existing static children, for the
		// same reason as the mirror-image case in addRoute.
		if len(n.children) > 0 {
			panic("gomicro: wildcard segment '" + wildcard + "' in new path '" + fullPath +
				"' conflicts with existing children of path segment '" + n.fullPath + "'")
		}

		if wildcard[0] == ':' { // param
			if i > 0 {
				n.path = path[:i]
				path = path[i:]
			}

			child := &node{
				nType:    param,
				path:     wildcard,
				fullPath: fullPath,
			}
			n.children = []*node{child}
			n.wildChild = true
			n = child
			n.priority++

			// Path continues past the param: another subpath starting with '/'.
			if len(wildcard) < len(path) {
				path = path[len(wildcard):]
				next := &node{priority: 1, fullPath: fullPath}
				n.children = []*node{next}
				n = next
				continue
			}

			n.handlers = handlers
			return
		}

		// catchAll
		if i+len(wildcard) != len(path) {
			panic("gomicro: catch-all routes are only allowed at the end of the path in path '" + fullPath + "'")
		}
		if len(n.path) > 0 && n.path[len(n.path)-1] == '/' {
			panic("gomicro: catch-all wildcard '" + wildcard + "' in new path '" + fullPath +
				"' conflicts with existing path segment '" + n.path + "'")
		}

		i-- // step back onto the '/' that must precede the catch-all
		if i < 0 || path[i] != '/' {
			panic("gomicro: no / before catch-all in path '" + fullPath + "'")
		}
		n.path = path[:i]

		// Empty placeholder node, reached through the '/' index...
		child := &node{
			wildChild: true,
			nType:     catchAll,
			fullPath:  fullPath,
		}
		n.children = []*node{child}
		n.indices = "/"
		n = child
		n.priority++

		// ...whose single child holds the variable and the handlers.
		n.children = []*node{{
			path:     path[i:],
			nType:    catchAll,
			handlers: handlers,
			priority: 1,
			fullPath: fullPath,
		}}
		return
	}

	// No wildcard: plain static leaf.
	n.path = path
	n.handlers = handlers
	n.fullPath = fullPath
}

// incrementChildPrio bumps the priority of the child at pos and keeps children
// sorted most-visited-first, so hot routes are found in fewer compares.
func (n *node) incrementChildPrio(pos int) int {
	cs := n.children
	cs[pos].priority++
	prio := cs[pos].priority

	newPos := pos
	for ; newPos > 0 && cs[newPos-1].priority < prio; newPos-- {
		cs[newPos-1], cs[newPos] = cs[newPos], cs[newPos-1]
	}

	if newPos != pos {
		n.indices = n.indices[:newPos] + n.indices[pos:pos+1] +
			n.indices[newPos:pos] + n.indices[pos+1:]
	}
	return newPos
}

// getValue matches path against the tree and returns the handlers, the
// extracted params and whether a trailing-slash redirect would match instead.
// params is only meaningful when handlers is non-nil.
func (n *node) getValue(path string) (handlers []HandlerFunc, params []Param, tsr bool) {
	return n.getValueInto(path, nil)
}

// getValueInto is getValue with a caller-supplied param buffer. Nothing on this
// path allocates as long as the buffer has capacity for the route's params.
func (n *node) getValueInto(path string, ps []Param) (handlers []HandlerFunc, params []Param, tsr bool) {
walk:
	for {
		prefix := n.path
		if len(path) > len(prefix) {
			if path[:len(prefix)] != prefix {
				break
			}
			path = path[len(prefix):]

			if !n.wildChild {
				idxc := path[0]
				for i := 0; i < len(n.indices); i++ {
					if n.indices[i] == idxc {
						n = n.children[i]
						continue walk
					}
				}
				// Nothing matched: a trailing slash is all that is left to try.
				tsr = path == "/" && n.handlers != nil
				return
			}

			// A wildcard is the only child of its parent, so there is nothing
			// to disambiguate here: conflicting siblings panic at insert time.
			n = n.children[len(n.children)-1]
			switch n.nType {
			case param:
				end := 0
				for end < len(path) && path[end] != '/' {
					end++
				}
				ps = append(ps, Param{Key: n.path[1:], Value: path[:end]})

				if end < len(path) {
					if len(n.children) > 0 {
						path = path[end:]
						n = n.children[0]
						continue walk
					}
					// Path continues but the tree does not.
					tsr = len(path) == end+1
					return
				}

				if handlers = n.handlers; handlers != nil {
					params = ps
					return
				}
				if len(n.children) == 1 {
					// Suggest the trailing slash if that variant is registered.
					n = n.children[0]
					tsr = (n.path == "/" && n.handlers != nil) ||
						(n.path == "" && n.indices == "/")
				}
				return

			case catchAll:
				ps = append(ps, Param{Key: n.path[2:], Value: path})
				handlers = n.handlers
				params = ps
				return

			default:
				panic("gomicro: invalid node type")
			}
		}

		if path == prefix {
			if handlers = n.handlers; handlers != nil {
				params = ps
				return
			}
			// No handler here, but "path + '/'" may be registered: that covers
			// both a plain trailing-slash route and a catch-all whose slash is
			// part of the wildcard.
			for i := 0; i < len(n.indices); i++ {
				if n.indices[i] == '/' {
					c := n.children[i]
					tsr = (len(c.path) == 1 && c.handlers != nil) ||
						(c.nType == catchAll && c.children[0].handlers != nil)
					return
				}
			}
			return
		}

		break
	}

	// Recommend the same URL with a trailing slash added or removed if a leaf
	// exists for that variant.
	tsr = path == "/" ||
		(len(n.path) == len(path)+1 && n.path[len(path)] == '/' &&
			path == n.path[:len(path)] && n.handlers != nil)
	return
}

// findWildcard returns the first wildcard segment in path, its start index, and
// whether it is well formed (exactly one ':' or '*' in the segment).
func findWildcard(path string) (wildcard string, i int, valid bool) {
	for start := 0; start < len(path); start++ {
		c := path[start]
		if c != ':' && c != '*' {
			continue
		}
		valid = true
		for end, d := range []byte(path[start+1:]) {
			switch d {
			case '/':
				return path[start : start+1+end], start, valid
			case ':', '*':
				valid = false
			}
		}
		return path[start:], start, valid
	}
	return "", -1, false
}

func longestCommonPrefix(a, b string) int {
	i, max := 0, len(a)
	if len(b) < max {
		max = len(b)
	}
	for i < max && a[i] == b[i] {
		i++
	}
	return i
}
