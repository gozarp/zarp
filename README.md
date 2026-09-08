# gomicro

A lightweight Go REST framework. Gin-style ergonomics, stdlib-level performance overhead.

No third-party dependencies. No reflection, no maps, and no allocations on the request path.

> **Status: pre-alpha, under construction.** The radix router, the pooled request context, the
> `Engine`, route groups and the middleware chain are built and tested — the code under
> [Using it today](#using-it-today) runs, including `NoRoute`, 405 handling, trailing-slash
> redirects and `Run`. Still missing: the `binding/`, `render/` and `middleware/` packages.
> See [Roadmap](#roadmap) for exactly where things stand.


---

## Why another router

Most Go frameworks pay for their ergonomics on every single request — a `map[string]string`
of route params, a closure allocated per middleware, a reflection call to render JSON. Those
costs are individually small and collectively the reason a framework benchmarks 5–10× worse
than `net/http`.

gomicro's design goals are non-negotiable constraints, not preferences:

| Goal | Rejected alternative | Why |
|---|---|---|
| **Radix tree routing** | regex, linear scan | O(path length) matching per method, no backtracking |
| **`sync.Pool` for `Context`** | allocate per request | zero per-request allocation for the context object |
| **Params as `[]Param{Key, Value}`** | `map[string]string` | maps are the #1 GC pressure source in naive routers |
| **Index-cursor middleware** | `func(http.Handler) http.Handler` nesting | no closure allocation, no stack frame per middleware |
| **No reflection in the hot path** | struct-tag magic everywhere | validation is opt-in and lives outside core |
| **No rendering engine in core** | built-in templates | HTML/XML are add-on packages, not dependencies |

Anything that adds an allocation, a reflection call, or a map lookup to the hot request path
needs an explicit justification in the PR description.

---

## Performance

Route resolution, measured against `net/http.ServeMux` on the same 12-route API-shaped route
set (static routes, single and multi params, one catch-all):

| Path | gomicro | `net/http.ServeMux` | |
|---|---|---|---|
| `/search/issues` (static) | **14.7 ns**, 0 allocs | 129.7 ns, 0 allocs | 8.8× |
| `/users/octocat` (1 param) | **18.4 ns**, 0 allocs | 147.6 ns, 1 alloc | 8.0× |
| `/repos/golang/go/issues/42` (3 params) | **37.0 ns**, 0 allocs | 316.3 ns, 3 allocs | 8.5× |
| `/repos/golang/go/contents/src/a.go` (catch-all) | **35.0 ns**, 0 allocs | 721.5 ns, 9 allocs | 20.6× |
| `/does/not/exist` (miss) | **12.2 ns**, 0 allocs | 501.3 ns, 9 allocs | 41× |

`go1.25.0 windows/amd64`, AMD Ryzen 7 7435HS, `-count=5`, averaged. Reproduce with:

```sh
go test -bench='Router|ServeMux' -benchmem -run XXX -count=5 .
```

**Read these fairly.** `ServeMux.Handler()` does more than match a tree — it also cleans the
path, handles host matching, and stores the matched wildcards in the request context, which is
where most of its allocations come from. gomicro's tree walk resolves the route and fills a
caller-supplied params buffer, leaving the rest to `Engine`. So read this table as *route
resolution is allocation-free and roughly an order of magnitude cheaper*, not as a claim about
whole requests. The end-to-end comparison below is the one to judge the framework by.

End to end through `ServeHTTP`, against `net/http.ServeMux` serving equivalent handlers:

| | gomicro | `net/http.ServeMux` |
|---|---|---|
| static route | **50.7 ns**, 0 allocs | 88.8 ns, 1 alloc |
| param route | **57.6 ns**, 0 allocs | 174.3 ns, 2 allocs |
| 404 | **47.5 ns**, 0 allocs | — |
| trailing-slash redirect | **28.6 ns**, 0 allocs | — |
| parallel (`RunParallel`, 16 cores) | **8.4 ns**, 0 allocs | — |

Zero allocations per request, end to end. Middleware costs **2.8 ns per hop** and allocates
nothing: 46.5 ns with none, 74.4 ns with ten. The pooled context resets in **3.4 ns**.

```sh
go test -bench=. -benchmem ./benchmarks/...
```

---

## How it works

### The radix tree

A radix tree (compressed trie) collapses every non-branching chain of nodes into one node
holding the whole shared substring. Routes share long prefixes, so this keeps the tree shallow
— matching is a handful of string comparisons rather than one node hop per character.

Registering `/user/list`, `/user/new` and `/post/list`:

```
/                     root
├── user/             indices: "ln"
│   ├── list
│   └── new
└── post/list
```

Matching `/user/new` compares `/`, then `user/`, then scans one byte to pick the child, then
compares `new`. No hashing, no allocation, no backtracking.

**`indices` is what replaces the map.** Each node keeps a string of the first byte of each
static child, positionally aligned with the children slice. Picking the next child is a scan
over a few bytes in one cache line — cheaper than computing a hash. Children are kept sorted
by subtree priority, so the hottest branch is usually the first byte tested.

**Params never touch a map.** They are a flat `[]Param{Key, Value}` slice, supplied by the
caller and appended into, which is what makes a param lookup allocation-free. Routes carry a
handful of params at most, so a linear scan beats a hash on every realistic input.

**Catch-alls are three nodes**, not one: the static parent ending *before* the slash, an
empty-path intermediate, and the leaf holding `/*name`. The intermediate is what lets `/src/`
itself match, with the slash landing inside the wildcard value rather than being eaten by the
parent's prefix.

### The context

One `Context` is pooled per in-flight request. `sync.Pool` gives a per-P free list, so under
load a `Get` is usually a pointer bump with no lock and no allocation. Two rules keep that
correct:

- **`reset` clears every field.** A field left behind is a cross-request data leak — one user
  seeing another's values. A reflection-based test asserts this automatically and fails when a
  field is added without a matching `reset` line.
- **The `*Context` must not escape the handler.** After it returns to the pool another
  goroutine can own it. A handler spawning a goroutine must copy what it needs.

The response writer is embedded **by value**, so it pools with the context instead of costing
a second allocation. It records status and size for middleware to log, and defers the actual
`WriteHeader` call so a later handler in the chain can still change the status.

### The middleware chain

Middleware is a `[]HandlerFunc` walked by an index cursor on the context:

```go
c.index++
for c.index < int8(len(c.handlers)) {
    c.handlers[c.index](c)
    c.index++
}
```

A middleware wanting before/after behaviour calls `c.Next()` in its middle; the loop resumes
when the nested call returns, because the index is shared state. One wanting to stop the chain
calls `c.Abort()`, which sets the index above any legal chain length so the loop condition
fails at every level of the stack at once. Cost per middleware: one integer increment. The
`func(http.Handler) http.Handler` alternative adds a stack frame to every request instead.

---

## Using it today

This works now:

```go
import "github.com/subhanjanops/gomicro"

r := gomicro.New()

r.GET("/ping", func(c *gomicro.Context) {
    c.JSON(200, map[string]string{"message": "pong"})
})

api := r.Group("/api/v1", authMiddleware)
api.GET("/users/:id", func(c *gomicro.Context) {
    c.JSON(200, User{ID: c.Param("id")})
})

r.Run(":8080") // or build your own http.Server — Engine is an http.Handler
```

Middleware is an ordinary handler that calls `Next`:

```go
func authMiddleware(c *gomicro.Context) {
    if c.GetHeader("Authorization") == "" {
        c.AbortWithStatusJSON(401, map[string]string{"error": "unauthorized"})
        return
    }
    c.Next()
}
```

Two notes on the response helpers: use `c.Text(code, s)` for text that comes from a variable —
`c.String` treats its argument as a format template, so `go vet` reports it and the `fmt` path
costs an allocation. And a route pattern is available as `c.FullPath()`, which is what you want
in logs and metrics rather than the raw URL.

---

## Repository layout

```
gomicro/
├── doc.go              package documentation
├── router.go           radix tree: node, addRoute, Lookup
├── context.go          Context, pooling, params, response helpers
├── engine.go           Engine, sync.Pool, ServeHTTP, 404/405/redirects
├── group.go            RouterGroup, verbs, prefixes, Use, IRouter
├── handler.go          HandlerFunc, Next, Abort
├── benchmarks/         end-to-end perf suite, run with -bench
├── binding/            request parsing/validation, optional import (empty)
├── render/             response rendering, optional import (empty)
├── middleware/         logger, recovery, cors, requestid (empty)
├── internal/bytesconv/ unsafe []byte<->string helpers (empty)
├── examples/           runnable example apps (empty)
├── CLAUDE.md           design constraints and conventions
└── IMPLEMENTATION_GUIDE.md   package-by-package build spec
```

[IMPLEMENTATION_GUIDE.md](IMPLEMENTATION_GUIDE.md) is the detailed spec: data structures,
algorithms, edge cases, required tests and acceptance criteria for every package.

---

## Development

```sh
go build ./...
go vet ./...
gofmt -l .                                   # should print nothing

go test ./...                                # unit tests; benchmarks/ has none
go test -run TestAddRouteLookup .            # a single test
go test -race ./...                          # see the note below on Windows

go test -bench=. -benchmem ./benchmarks/...  # end-to-end suite
go test -bench=. -benchmem -run XXX .        # router and context micro-benchmarks
go test -bench=BenchmarkRouterParam -benchmem -count=10 -run XXX . > new.txt
benchstat old.txt new.txt                    # required for perf-sensitive changes
```

On Windows, ThreadSanitizer may fail to start (`ThreadSanitizer failed to allocate ...
error code: 87`). Run the race detector in Linux instead:

```sh
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W)":/src -w /src golang:1.25 go test -race ./...
```

### PR checklist

- [ ] `go build ./... && go vet ./... && gofmt -l .` (last prints nothing)
- [ ] `go test ./...` and `go test -race ./...` pass
- [ ] `benchstat` comparison in the description for any change to the router, context or chain
- [ ] allocs/op did not increase for any benchmark
- [ ] no new map, reflection call, or `fmt` call on the hot path — or a written justification
- [ ] a new `Context` field is cleared in `reset`

---

## Roadmap

| | Milestone | State |
|---|---|---|
| M1 | Radix router — `addRoute`, `Lookup`, wildcards, TSR, priority ordering | **done** — 18 tests, 0 allocs, benchmarked vs `ServeMux` |
| M2 | `Context` — pooling, `reset`, params/query/form/keys, response helpers, `Copy` | **done** — accessors are allocation-free |
| M3 | `Engine` — `ServeHTTP`, `sync.Pool` wiring, 404/405/redirects | **done** — `NoRoute`, 405 + `Allow`, trailing-slash redirects, `Run`, race-clean pooling |
| M4 | `RouterGroup` + chain execution — nesting, `Next`, `Abort`, `IRouter`/`IRoutes` | **done** |
| — | Flat root package, `benchmarks/` split out, godoc pass | **done** |
| M5 | `binding/` + `render/` | not started |
| M6 | `middleware/` — logger, recovery, cors, requestid | not started |
| M7 | `examples/`, published baseline numbers | not started |

A milestone starts only when the previous one's tests and benchmarks pass. The whole value
proposition here is a performance claim, and a claim never measured at each layer cannot be
attributed to a layer when it regresses.

---

## License

MIT — see [LICENSE](LICENSE).
