package gomicro

type Param struct {
	Key, Value string
}

type Params []Param

func (p Params) Get(name string) (string, bool) {
	for _, param := range p {
		if param.Key == name {
			return param.Value, true
		}
	}
	return "", false
}

func (p Params) ByName(name string) string {
	for _, param := range p {
		if param.Key == name {
			return param.Value
		}
	}
	return ""
}

type nodeType uint8

const (
	static nodeType = iota
	root
	param
	catchAll
)

type node struct {
	path, indices, fullPath string
	children                []*node
	handlers                []HandlerFunc
	priority                uint32
	wildChild               bool
	nType                   nodeType
}

// insertChild fills n with path, creating whatever further nodes the path
// needs below it, and attaches handlers to the node that terminates the route.
//
// n must be an empty node — a freshly created child from addRoute, or the root
// of an empty tree. insertChild owns every wildcard syntax rule and panics on a
// malformed or conflicting one; registration is boot-time, so a panic is the
// right signal.
func (n *node) insertChild(path, fullPath string, handlers []HandlerFunc) {
	for {
		wildcard, i, valid := findWildcard(path)
		if i < 0 {
			break
		}
		if !valid {
			panic("gomicro: only one wildcard per path segment is allowed, has: '" +
				wildcard + "' in path '" + fullPath + "'")
		}
		if len(wildcard) < 2 {
			panic("gomicro: wildcards must be named with a non-empty name in path '" +
				fullPath + "'")
		}
		if len(n.children) > 0 {
			panic("gomicro: wildcard segment '" + wildcard +
				"' conflicts with existing children in path '" + fullPath + "'")
		}

		if wildcard[0] == ':' {
			// Static text before the wildcard stays on this node.
			if i > 0 {
				n.path = path[:i]
				path = path[i:]
			}

			child := &node{nType: param, path: wildcard, fullPath: fullPath, priority: 1}
			n.children = []*node{child}
			n.wildChild = true
			n = child

			// The route continues past the param: the remainder, which starts
			// with '/', hangs off the param node.
			if len(wildcard) < len(path) {
				path = path[len(wildcard):]
				next := &node{fullPath: fullPath, priority: 1}
				n.children = []*node{next}
				n = next
				continue
			}

			n.handlers = handlers
			return
		}

		// A catch-all must end the path and be preceded by '/'.
		if i+len(wildcard) != len(path) {
			panic("gomicro: catch-all routes are only allowed at the end of the path in path '" +
				fullPath + "'")
		}
		if i == 0 || path[i-1] != '/' {
			panic("gomicro: no / before catch-all in path '" + fullPath + "'")
		}

		// Three nodes: the static prefix ending *before* the '/', an empty
		// intermediate so the prefix plus a bare '/' still matches, and the leaf
		// holding "/*name". getValue's catchAll case reads the key as path[2:],
		// so the leading slash must live on the leaf.
		n.path = path[:i-1]
		n.indices = "/"
		n.fullPath = fullPath

		mid := &node{nType: catchAll, wildChild: true, fullPath: fullPath, priority: 1}
		n.children = []*node{mid}
		mid.children = []*node{{
			path:     path[i-1:],
			nType:    catchAll,
			handlers: handlers,
			fullPath: fullPath,
			priority: 1,
		}}
		return
	}

	// No wildcard: one static node holding the whole remainder.
	n.path = path
	n.handlers = handlers
	n.fullPath = fullPath
}

// incrementChildPrio bumps the priority of the child at pos, then moves it left
// past every sibling it now outranks so the hottest subtrees are scanned first.
// children and indices are reordered together — they are positionally aligned
// and getValue trusts that.
//
// It returns the child's new index. Callers must re-seat on the return value:
// the child has usually moved.
func (n *node) incrementChildPrio(pos int) int {
	cs := n.children
	cs[pos].priority++
	prio := cs[pos].priority

	newPos := pos
	for ; newPos > 0 && cs[newPos-1].priority < prio; newPos-- {
		cs[newPos-1], cs[newPos] = cs[newPos], cs[newPos-1]
	}

	if newPos != pos {
		n.indices = n.indices[:newPos] + // unchanged prefix
			n.indices[pos:pos+1] + // the byte that moved
			n.indices[newPos:pos] + // the bytes it jumped over
			n.indices[pos+1:] // unchanged suffix
	}
	return newPos
}

// longestCommonPrefix returns the number of leading bytes a and b share, i.e.
// the index of the first byte at which they differ, bounded by the shorter of
// the two. Comparison is byte-wise, not rune-wise: a shared prefix may end
// mid-rune, which is harmless because lookup compares the same way.
func longestCommonPrefix(a, b string) int {
	n := min(len(a), len(b))
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}

// findWildcard returns the first wildcard segment in path — the marker byte
// (':' or '*') plus its name, up to the next '/' or the end of path — together
// with the byte index of the marker and whether the segment is well formed.
//
// i is -1 when path holds no wildcard. An invalid wildcard is still returned
// with its index so the caller can name it in a panic message: valid is false
// for an empty name (a bare ':' or '*') and for a second marker inside the same
// segment. Whether a wildcard is allowed at that position — a catch-all must be
// last and preceded by '/' — is the caller's rule, not this function's.
func findWildcard(path string) (wildcard string, i int, valid bool) {
	for start := 0; start < len(path); start++ {
		if path[start] != ':' && path[start] != '*' {
			continue
		}
		valid = true
		end := len(path)
	scan:
		for j := start + 1; j < len(path); j++ {
			switch path[j] {
			case '/':
				end = j
				break scan
			case ':', '*':
				valid = false
			}
		}
		wildcard = path[start:end]
		return wildcard, start, valid && len(wildcard) > 1
	}
	return "", -1, false
}

type methodTree struct {
	method string
	root   *node
}

type router struct {
	trees     []methodTree
	maxParams uint16
}

// countParams returns the number of wildcards in path, saturating at the width
// of maxParams rather than wrapping.
func countParams(path string) uint16 {
	var n uint16
	for i := 0; i < len(path); i++ {
		if path[i] == ':' || path[i] == '*' {
			if n == ^uint16(0) {
				return n
			}
			n++
		}
	}
	return n
}

// addRoute registers handlers for method and path, creating the method's tree
// on first use. It panics on a malformed route, a duplicate registration or a
// wildcard conflict: registration happens at boot, so a panic is the signal
// that fits.
func (r *router) addRoute(method, path string, handlers []HandlerFunc) {
	switch {
	case method == "":
		panic("gomicro: method must not be empty")
	case path == "" || path[0] != '/':
		panic("gomicro: path must begin with '/' in path '" + path + "'")
	case len(handlers) == 0:
		panic("gomicro: there must be at least one handler for path '" + path + "'")
	case len(handlers) >= int(abortIndex):
		panic("gomicro: too many handlers for path '" + path + "'")
	}

	// Sized once here so Engine can allocate a Context's Params buffer exactly.
	if c := countParams(path); c > r.maxParams {
		r.maxParams = c
	}

	for i := range r.trees {
		if r.trees[i].method == method {
			r.trees[i].root.addRoute(path, path, handlers)
			return
		}
	}

	root := &node{}
	r.trees = append(r.trees, methodTree{method: method, root: root})
	root.addRoute(path, path, handlers)
}

// addRoute walks the tree from n, consuming path, and hands whatever the tree
// does not already hold to insertChild. fullPath stays whole throughout, for
// panic messages and node bookkeeping.
func (n *node) addRoute(path, fullPath string, handlers []HandlerFunc) {
	n.priority++

	// An empty tree: insertChild builds the whole route, wildcards included.
	if n.path == "" && n.indices == "" {
		n.insertChild(path, fullPath, handlers)
		n.nType = root
		return
	}

walk:
	for {
		i := longestCommonPrefix(path, n.path)

		// This node's label runs past the shared prefix, so it has to become a
		// branch point: everything it holds moves down into one child.
		if i < len(n.path) {
			child := &node{
				path:      n.path[i:],
				indices:   n.indices,
				children:  n.children,
				handlers:  n.handlers,
				wildChild: n.wildChild,
				fullPath:  n.fullPath,
				nType:     static,
				priority:  n.priority - 1, // this insert's bump stays with the parent
			}
			n.children = []*node{child}
			n.indices = n.path[i : i+1]
			n.path = path[:i]
			n.handlers = nil
			n.wildChild = false
			// path is always a suffix of fullPath, so this is the route prefix
			// the node now stands for.
			n.fullPath = fullPath[:len(fullPath)-len(path)+i]
		}

		// The route continues past this node. Note this is not an else: a fork
		// mid-label is a split *and* a descent.
		if i < len(path) {
			path = path[i:]
			c := path[0]

			// The remainder of a route that continues past a param.
			if n.nType == param && c == '/' && len(n.children) == 1 {
				n = n.children[0]
				n.priority++
				continue walk
			}

			// An existing static child, found by its first byte.
			for idx := 0; idx < len(n.indices); idx++ {
				if c == n.indices[idx] {
					idx = n.incrementChildPrio(idx) // the child may have moved
					n = n.children[idx]
					continue walk
				}
			}

			if !n.wildChild && c != ':' && c != '*' && n.nType != catchAll {
				// A new static branch. Create the child empty and let
				// insertChild fill it: insertChild owns the node it is given.
				n.indices += string(c)
				child := &node{}
				n.children = append(n.children, child)
				n.incrementChildPrio(len(n.indices) - 1)
				n = child
			} else if n.wildChild {
				n = n.children[0]
				n.priority++

				// The new route may share this wildcard only if it is the same
				// one: same name, and the segment ends where it ended before.
				if len(path) >= len(n.path) && n.path == path[:len(n.path)] &&
					n.nType != catchAll &&
					(len(n.path) >= len(path) || path[len(n.path)] == '/') {
					continue walk
				}

				panic("gomicro: '" + path + "' in new path '" + fullPath +
					"' conflicts with existing wildcard '" + n.path +
					"' in existing prefix '" + n.fullPath + "'")
			}

			n.insertChild(path, fullPath, handlers)
			return
		}

		// Exact match: this node owns the route.
		if n.handlers != nil {
			panic("gomicro: handlers are already registered for path '" + fullPath + "'")
		}
		n.handlers = handlers
		n.fullPath = fullPath
		return
	}
}

// lookup matches path in the tree registered for method.
//
// params is supplied by the caller and appended into, so a param route costs no
// allocation; pass a zero-length slice whose backing array holds at least
// r.maxParams entries. It may be nil when the caller does not want params.
//
// fullPath is the route pattern that matched, e.g. "/user/:id", for logging and
// metrics.
//
// tsr ("trailing slash redirect") reports that path would have matched with a
// trailing slash added or removed. It is a hint only: lookup never writes a
// response, and whether to redirect is Engine's decision.
func (r *router) lookup(method, path string, params *Params) (handlers []HandlerFunc, fullPath string, tsr bool) {
	for i := range r.trees {
		if r.trees[i].method == method {
			return r.trees[i].root.getValue(path, params)
		}
	}
	return nil, "", false
}

// getValue walks the tree from n, consuming path as it descends.
func (n *node) getValue(path string, params *Params) (handlers []HandlerFunc, fullPath string, tsr bool) {
walk:
	for {
		prefix := n.path

		if len(path) > len(prefix) {
			if path[:len(prefix)] == prefix {
				path = path[len(prefix):]

				// No wildcard child: pick the next static child by its first
				// byte. The indices scan is what replaces a map lookup here.
				if !n.wildChild {
					idxc := path[0]
					for i := 0; i < len(n.indices); i++ {
						if n.indices[i] == idxc {
							n = n.children[i]
							continue walk
						}
					}
					// Nothing matched. The path may be this node's route with
					// one extra trailing slash.
					tsr = path == "/" && n.handlers != nil
					return nil, "", tsr
				}

				n = n.children[0]
				switch n.nType {
				case param:
					// The value runs to the next '/' or the end of path.
					end := 0
					for end < len(path) && path[end] != '/' {
						end++
					}

					if params != nil {
						*params = append(*params, Param{Key: n.path[1:], Value: path[:end]})
					}

					if end < len(path) {
						if len(n.children) > 0 {
							path = path[end:]
							n = n.children[0]
							continue walk
						}
						// Path continues but the tree does not.
						tsr = len(path) == end+1
						return nil, "", tsr
					}

					if n.handlers != nil {
						return n.handlers, n.fullPath, false
					}
					if len(n.children) == 1 {
						n = n.children[0]
						tsr = n.path == "/" && n.handlers != nil
					}
					return nil, "", tsr

				case catchAll:
					// Everything left, leading '/' included, is the value.
					if params != nil {
						*params = append(*params, Param{Key: n.path[2:], Value: path})
					}
					return n.handlers, n.fullPath, false

				default:
					panic("gomicro: invalid node type")
				}
			}
		} else if path == prefix {
			if n.handlers != nil {
				return n.handlers, n.fullPath, false
			}

			if path == "/" && n.wildChild && n.nType != root {
				return nil, "", true
			}

			// No handlers here: this path plus a trailing slash may be a route.
			for i := 0; i < len(n.indices); i++ {
				if n.indices[i] == '/' {
					n = n.children[i]
					tsr = (len(n.path) == 1 && n.handlers != nil) ||
						(n.nType == catchAll && n.children[0].handlers != nil)
					return nil, "", tsr
				}
			}
			return nil, "", false
		}

		// No match. Recommend a redirect to the same path with a trailing
		// slash if a leaf exists there.
		tsr = path == "/" ||
			(len(prefix) == len(path)+1 && prefix[len(path)] == '/' &&
				path == prefix[:len(path)] && n.handlers != nil)
		return nil, "", tsr
	}
}
