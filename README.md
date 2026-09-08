<h1 align="center">⚡ zarp</h1>

<p align="center">
  <em>A lightweight REST framework for Go — expressive routing and middleware at stdlib-level overhead.</em>
</p>

<p align="center">
  <a href="https://github.com/gozarp/zarp/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/gozarp/zarp/actions/workflows/ci.yml/badge.svg"></a>
  <a href="https://pkg.go.dev/github.com/gozarp/zarp"><img alt="Go Reference" src="https://pkg.go.dev/badge/github.com/gozarp/zarp.svg"></a>
  <a href="https://github.com/gozarp/zarp/actions/workflows/lint.yml"><img alt="Lint" src="https://github.com/gozarp/zarp/actions/workflows/lint.yml/badge.svg"></a>
  <a href="go.mod"><img alt="Go version" src="https://img.shields.io/github/go-mod/go-version/gozarp/zarp"></a>
  <a href="LICENSE"><img alt="License: MIT" src="https://img.shields.io/badge/license-MIT-green.svg"></a>
  <br>
  <img alt="Dependencies: zero" src="https://img.shields.io/badge/dependencies-zero-blue">
  <img alt="allocs/op: 0" src="https://img.shields.io/badge/allocs%2Fop-0-brightgreen">
  <img alt="Status: pre-alpha" src="https://img.shields.io/badge/status-pre--alpha-orange">
</p>

🪶 No third-party dependencies in any package. 🚫 No reflection, no maps, and no allocations on
the request path.

```go
r := zarp.New()
r.GET("/users/:id", func(c *zarp.Context) {
    c.JSON(200, User{ID: c.Param("id")})
})
r.Run(":8080")
```

> 🚧 **Status: pre-alpha.** Everything documented here is built, tested and benchmarked — 229
> tests, clean under `-race`, allocation-free end to end. The API is not yet frozen and there is
> no tagged release, so pin a commit if you depend on it today.

---

## 📦 Installation

```sh
go get github.com/gozarp/zarp
```

Requires Go 1.25 or later.

---

## 🚀 Quick start

```go
package main

import "github.com/gozarp/zarp"

func main() {
    r := zarp.New()

    r.GET("/ping", func(c *zarp.Context) {
        c.JSON(200, map[string]string{"message": "pong"})
    })

    api := r.Group("/api/v1", authMiddleware)
    api.GET("/users/:id", func(c *zarp.Context) {
        c.JSON(200, User{ID: c.Param("id")})
    })

    r.Run(":8080") // or build your own http.Server — Engine is an http.Handler
}

func authMiddleware(c *zarp.Context) {
    if c.GetHeader("Authorization") == "" {
        c.AbortWithStatusJSON(401, map[string]string{"error": "unauthorized"})
        return
    }
    c.Next()
}
```

Middleware is an ordinary handler that calls `c.Next()`. Runnable programs live in
[examples/](examples/): `hello`, `params`, `middleware`, `restapi`, `graceful`.

**Two conventions worth knowing.** Use `c.Text(code, s)` for text that comes from a variable —
`c.String` treats its first argument as a format template, so `go vet` flags it and the `fmt`
path costs an allocation. And prefer `c.FullPath()` — the matched route pattern — over the raw
URL in logs and metrics.

---

## 🤔 Why another router

Ergonomics are usually paid for on every request: a `map[string]string` of route params, a
closure allocated per middleware, a reflection call to render JSON. Each cost is small on its
own; together they are what separates a routing layer's throughput from the standard library's.

zarp treats the following as constraints, not preferences:

| Constraint | Rejected alternative | Rationale |
|---|---|---|
| **Radix tree routing** | regex, linear scan | O(path length) matching per method, no backtracking |
| **`sync.Pool` for `Context`** | allocate per request | zero per-request allocation for the context object |
| **Params as `[]Param{Key, Value}`** | `map[string]string` | maps are the largest GC pressure source in naive routers |
| **Index-cursor middleware** | `func(http.Handler) http.Handler` | no closure allocation, no stack frame per layer |
| **No reflection in the hot path** | struct-tag magic everywhere | validation is opt-in and lives outside core |
| **No rendering engine in core** | built-in templates | HTML/XML are add-on packages, not dependencies |

Any change that adds an allocation, a reflection call, or a map lookup to the hot request path
requires an explicit justification in the pull request description.

---

## 📊 Performance

The checked-in baseline is [benchmarks/BASELINE.md](benchmarks/BASELINE.md);
`go1.25.0 windows/amd64`, AMD Ryzen 7 7435HS, `-count=5`, averaged.

**End to end** — a whole request through `ServeHTTP`: pool, route, chain, handler, response.

| | zarp | `net/http.ServeMux` |
|---|---|---|
| static route | **52.4 ns**, 0 allocs | 94.4 ns, 1 alloc |
| param route | **59.9 ns**, 0 allocs | 186.7 ns, 2 allocs |
| grouped route, 2 middlewares | **62.6 ns**, 0 allocs | — |
| 404 | **56.4 ns**, 0 allocs | — |
| trailing-slash redirect | **30.3 ns**, 0 allocs | — |
| parallel, 16 cores | **8.0 ns**, 0 allocs | — |

**Route resolution alone**, over a 12-route API-shaped set:

| | zarp | `net/http.ServeMux` | |
|---|---|---|---|
| static | **15.8 ns**, 0 allocs | 139.3 ns, 0 allocs | 8.8× |
| 1 param | **18.7 ns**, 0 allocs | 163.3 ns, 1 alloc | 8.7× |
| 3 params | **38.7 ns**, 0 allocs | 364.8 ns, 3 allocs | 9.4× |
| catch-all | **36.3 ns**, 0 allocs | 884.2 ns, 9 allocs | 24× |
| miss | **11.4 ns**, 0 allocs | 575.5 ns, 9 allocs | 50× |

**Read the second table fairly.** `ServeMux.Handler()` does more than match a tree — it cleans
the path, matches hosts, and stores wildcards in the request context, which is where most of its
allocations come from. zarp's tree walk resolves the route into a caller-supplied params
buffer and leaves the rest to `Engine`. Read it as *route resolution is allocation-free and
roughly an order of magnitude cheaper*, not as a claim about whole requests. The end-to-end table
is the one to judge zarp by.

Middleware costs **~2.8 ns per hop** and allocates nothing (51.9 ns with none, 78.4 ns with ten).
The pooled context resets in **3.9 ns**. The one core path that allocates is 405 responses, which
walk the other method trees to build the `Allow` header — which is why `HandleMethodNotAllowed`
is off by default.

```sh
go test -bench=. -benchmem -run XXX -count=5 ./benchmarks/       # end to end
go test -bench='Router|ServeMux|Context' -benchmem -run XXX .    # micro-benchmarks
```

---

## 🧩 Features

**Core** (`zarp`, stdlib only)

- Radix-tree router with `:param` and `*catchall` wildcards, priority ordering and trailing-slash
  redirects
- Pooled `Context` implementing `context.Context`, with params, query, form, multipart, a typed
  key/value store, `Copy()` for goroutines, and response helpers (`JSON`, `Text`, `Data`,
  `Redirect`)
- Route groups with prefix and middleware nesting; `GET`/`POST`/`PUT`/`PATCH`/`DELETE`/`HEAD`/
  `OPTIONS`/`Any`/`Handle`
- `NoRoute` and `NoMethod` fallbacks, `Static`, `StaticFS`, `StaticFile`, and `Run`

**Optional packages** (import only what you use)

| Package | Contents |
|---|---|
| [binding/](binding/) | JSON, query, form, multipart, URI and header binders; opt-in struct-tag validation |
| [render/](render/) | JSON, indented JSON, XML, HTML, text and redirect renderers |
| [middleware/](middleware/) | `Logger`, `Recovery`, `CORS`, `RequestID` |

The request logger writes plain text to stderr and depends on no logging library. Any logger
plugs in through `Sink`, a single-method interface:

```go
r.Use(middleware.LoggerWith(middleware.SinkFunc(func(e middleware.Entry) {
    // e carries Method, Path (the route pattern), Status, Latency, Size, ClientIP
    // and Level(), which maps status to severity.
})))
```

`middleware.SlogSink` ships for `log/slog`. For a structured logger that is faster on this path
and allocates nothing, [loggy](https://github.com/subhanjanOps/loggy) is worth a look — its
adapter is about ten lines, and the shape is documented on `Sink`.

---

## 🔬 How it works

### 🌳 The radix tree

A radix tree (compressed trie) collapses every non-branching chain of nodes into one node holding
the whole shared substring. Routes share long prefixes, so the tree stays shallow — matching is a
handful of string comparisons rather than one node hop per character.

Registering `/user/list`, `/user/new` and `/post/list`:

```
/                     root
├── user/             indices: "ln"
│   ├── list
│   └── new
└── post/list
```

Matching `/user/new` compares `/`, then `user/`, scans one byte to pick the child, then compares
`new`. No hashing, no allocation, no backtracking.

**`indices` is what replaces the map.** Each node keeps a string of the first byte of each static
child, positionally aligned with the children slice. Picking the next child is a scan over a few
bytes in one cache line — cheaper than computing a hash. Children are sorted by subtree priority,
so the hottest branch is usually the first byte tested.

**Params never touch a map.** They are a flat `[]Param{Key, Value}` slice supplied by the caller
and appended into, which is what makes param lookup allocation-free. Routes carry a handful of
params at most, so a linear scan beats a hash on every realistic input.

**Catch-alls are three nodes**, not one: the static parent ending *before* the slash, an
empty-path intermediate, and the leaf holding `/*name`. The intermediate is what lets `/src/`
itself match, with the slash landing inside the wildcard value rather than being eaten by the
parent's prefix.

### ♻️ The context

One `Context` is pooled per in-flight request. `sync.Pool` gives a per-P free list, so under load
a `Get` is usually a pointer bump with no lock and no allocation. Two rules keep that correct:

- **`reset` clears every field.** A field left behind is a cross-request data leak — one user
  seeing another's values. A reflection-based test asserts this and fails when a field is added
  without a matching `reset` line.
- **The `*Context` must not escape the handler.** Once it returns to the pool, another goroutine
  can own it; a handler spawning a goroutine must `Copy()` what it needs.

The response writer is embedded **by value**, so it pools with the context instead of costing a
second allocation. It records status and size for middleware to log, and defers the actual
`WriteHeader` call so a later handler in the chain can still change the status.

### 🔗 The middleware chain

Middleware is a `[]HandlerFunc` walked by an index cursor on the context:

```go
c.index++
for c.index < int8(len(c.handlers)) {
    c.handlers[c.index](c)
    c.index++
}
```

A middleware wanting before/after behaviour calls `c.Next()` in its middle; the loop resumes when
the nested call returns, because the index is shared state. One wanting to stop the chain calls
`c.Abort()`, which sets the index above any legal chain length so the loop condition fails at
every level of the stack at once. Cost per layer: one integer increment. The
`func(http.Handler) http.Handler` alternative adds a stack frame to every request instead.

---

## 🗂️ Repository layout

```
zarp/
├── doc.go                     package documentation
├── router.go                  radix tree: node, addRoute, Lookup
├── context.go                 Context, pooling, params, response helpers
├── engine.go                  Engine, sync.Pool, ServeHTTP, 404/405/redirects
├── group.go                   RouterGroup, verbs, prefixes, Use, IRouter
├── handler.go                 HandlerFunc, Next, Abort
├── static.go                  Static, StaticFS, StaticFile
├── internal/bytesconv/        unsafe []byte↔string helpers, module-private
├── binding/                   request parsing and validation, optional import
├── render/                    JSON/XML/HTML/text rendering, optional import
├── middleware/                logger, recovery, cors, requestid
├── examples/                  five runnable apps
├── benchmarks/                end-to-end perf suite plus BASELINE.md
├── CONTRIBUTING.md            how to work on it
└── CLAUDE.md                  design constraints and conventions
```

---

## 🛠️ Development

```sh
go build ./...
go vet ./...
gofmt -l .                                         # should print nothing
golangci-lint run ./...                            # config in .golangci.yml

go test ./...                                      # unit tests; benchmarks/ has none
go test -run TestAddRouteLookup .                  # a single test
go test -race ./...                                # see the Windows note below
go test -cover . ./binding ./render ./middleware   # CI gates this at 90%

go test -bench=. -benchmem ./benchmarks/...
go test -bench=BenchmarkRouterParam -benchmem -count=10 -run XXX . > new.txt
benchstat old.txt new.txt                          # required for perf-sensitive changes
```

On Windows, ThreadSanitizer may fail to start (`ThreadSanitizer failed to allocate ... error
code: 87`). Run the race detector under Linux instead:

```sh
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W)":/src -w /src golang:1.25 go test -race ./...
```

---

## 🤝 Contributing

Bug reports and pull requests are welcome. [CONTRIBUTING.md](CONTRIBUTING.md) covers the design
constraints a change is held to, the `benchstat` comparison required for anything touching the
router, context or middleware chain, and the pre-PR checklist.

Please open an issue first for anything that changes the public API, adds a package, or touches
the router.

---

## 📄 License

MIT — see [LICENSE](LICENSE).
