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
  <img alt="Coverage: 96%" src="https://img.shields.io/badge/coverage-96%25-brightgreen">
  <img alt="Release: v0.1.0" src="https://img.shields.io/badge/release-v0.1.0-blue">
  <img alt="Status: pre-alpha" src="https://img.shields.io/badge/status-pre--alpha-orange">
</p>

🪶 No third-party dependencies in any package. 🚫 No reflection and no map lookups in the router,
and a request that routes, runs its chain and writes a response allocates nothing.

```go
r := zarp.New()
r.GET("/users/:id", func(c *zarp.Context) {
    c.JSON(200, User{ID: c.Param("id")})
})
r.Run(":8080")
```

---

## 🚦 Status

**Pre-alpha, and honest about it.** Everything documented below is built, tested and measured:

| | |
|---|---|
| **Tests** | 283, passing, clean under `-race` |
| **Coverage** | 96% across `.`, `binding/`, `render/`, `middleware/` — CI gates at 90% |
| **Allocations** | 0 on the routing, chain and response paths the benchmarks cover |
| **Dependencies** | none, in any package — asserted by CI, not by convention |
| **Security scanning** | `gosec` and `govulncheck` run on every push |
| **API** | **not frozen** — `v0.1.0` is a `v0.x` tag |

`v0.1.0` is tagged, so `go get github.com/gozarp/zarp@v0.1.0` pins a real version rather than a
commit. The API is still not frozen: before `v1.0.0`, an exported symbol can be renamed or
resigned, and the release notes will say so when it happens.

The pre-tag window was used deliberately to fix defaults and signatures that would be expensive to
change later. What that bought: proxy headers are no longer trusted by default, request binding no
longer validates behind your back, media types match case-insensitively, JSON bodies are capped and
rejected if they carry a second document, and `Redirect` no longer accepts a status that is not a
redirect. Those were all breaking changes, and all cheaper then than now.

Full notes in [`CHANGELOG/CHANGELOG-0.1.0.md`](CHANGELOG/CHANGELOG-0.1.0.md); what comes next is in
[`ROADMAP.md`](ROADMAP.md).

---

## 📦 Installation

```sh
go get github.com/gozarp/zarp@v0.1.0
```

Go 1.25 or later — and keep the patch current, since `Engine.Run` reaches standard-library code
fixed in go1.25.3. The core package imports only the standard library; `binding/`, `render/` and
`middleware/` are separate imports you opt into.

---

## 🚀 Quick start

```go
package main

import (
    "log"
    "net/http"

    "github.com/gozarp/zarp"
    "github.com/gozarp/zarp/middleware"
)

type User struct {
    ID   string `json:"id"`
    Name string `json:"name"`
}

func main() {
    r := zarp.New()

    // Logger then Recovery, so the logger records the 500 recovery produces.
    r.Use(middleware.Default()...)

    r.GET("/ping", func(c *zarp.Context) {
        c.JSON(http.StatusOK, map[string]string{"message": "pong"})
    })

    api := r.Group("/api/v1", authMiddleware)
    api.GET("/users/:id", func(c *zarp.Context) {
        c.JSON(http.StatusOK, User{ID: c.Param("id"), Name: "octocat"})
    })

    // Run is for development: it leaves every timeout at its zero value.
    // Production builds its own http.Server — see examples/graceful.
    log.Fatal(r.Run(":8080"))
}

func authMiddleware(c *zarp.Context) {
    if c.GetHeader("Authorization") == "" {
        // Abort, not return: returning does not stop the chain.
        c.AbortWithStatusJSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
        return
    }
    c.Next()
}
```

---

## 📚 Examples

Five runnable programs in [examples/](examples/), smallest first. Each is a single `main.go` with
the commands to exercise it in its header comment.

| | Run it | What it shows |
|---|---|---|
| [hello](examples/hello) | `go run ./examples/hello` | The smallest server there is: one route, one handler |
| [params](examples/params) | `go run ./examples/params` | The three ways a route carries data — path parameters, a catch-all, and the query string |
| [middleware](examples/middleware) | `go run ./examples/middleware` | The chain: stock middleware, groups, a custom auth guard that aborts, and a logger sink pointed at `log/slog` |
| [restapi](examples/restapi) | `go run ./examples/restapi` | CRUD over an in-memory store, using `binding/` for requests and `render/` for responses — including how to answer 400, 415 and 422 differently |
| [graceful](examples/graceful) | `go run ./examples/graceful` | What a production server actually looks like: your own `http.Server` with timeouts, and a shutdown that lets in-flight requests finish |

---

## 🧭 Routing

The router is a radix tree per HTTP method. Registration is a startup activity — see
[Lifecycle](#-lifecycle-configure-then-serve).

```go
r := zarp.New()

// Static, parameters, and a catch-all.
r.GET("/health", health)
r.GET("/users/:id", showUser)                  // c.Param("id")
r.GET("/repos/:owner/:repo/issues/:num", show) // several in one path
r.GET("/files/*filepath", serveFile)           // c.Param("filepath") == "/a/b.txt"

// Every verb, plus Handle for anything else.
r.POST("/users", createUser)
r.PUT("/users/:id", replaceUser)
r.PATCH("/users/:id", updateUser)
r.DELETE("/users/:id", deleteUser)
r.HEAD("/users/:id", headUser)
r.OPTIONS("/users", optionsUsers)
r.Any("/webhook", webhook)                     // all nine methods
r.Handle("BASELINE-CONTROL", "/wd", handler)   // any RFC 9110 method token
```

### Groups

A group carries a path prefix and a middleware chain. Nesting composes both.

```go
api := r.Group("/api/v1")
api.Use(middleware.Timeout(5 * time.Second))

public := api.Group("/public")
public.GET("/status", status)

admin := api.Group("/admin", requireAdmin)   // middleware at construction
admin.DELETE("/users/:id", deleteUser)       // → DELETE /api/v1/admin/users/:id
```

### Fallbacks

```go
r.NoRoute(func(c *zarp.Context) {
    c.JSON(http.StatusNotFound, map[string]string{"error": "no such endpoint"})
})

// 405 is opt-in: it probes every other method tree on a miss, and allocates
// to build the Allow header.
r.HandleMethodNotAllowed = true
r.NoMethod(func(c *zarp.Context) {
    c.JSON(http.StatusMethodNotAllowed, map[string]string{"allow": c.Writer.Header().Get("Allow")})
})
```

A handler that matched a route but found nothing behind it can hand the request to the same
fallback rather than inventing its own 404:

```go
func showUser(c *zarp.Context) {
    user, ok := store.Get(c.Param("id"))
    if !ok {
        c.NotFound()   // runs NoRoute, exactly as an unmatched path would
        return
    }
    c.JSON(http.StatusOK, user)
}
```

### Static files

```go
r.Static("/assets", "./public")             // built on a catch-all route
r.StaticFile("/favicon.ico", "./favicon.ico")
r.StaticFS("/embedded", http.FS(embeddedFS))

// When the directory is not entirely yours — uploads, an extracted archive,
// a repository checkout — SecureDir refuses paths whose symlinks resolve
// outside the root. http.Dir blocks "../" but follows links anywhere.
r.StaticFS("/uploads", zarp.SecureDir("./uploads"))
```

---

## 🧰 The Context

One `Context` per in-flight request, pooled. It implements `context.Context`, so it goes straight
into anything that takes one.

```go
func handler(c *zarp.Context) {
    // Request
    id    := c.Param("id")                       // route parameter
    page  := c.DefaultQuery("page", "1")         // query string, with a default
    tags  := c.QueryArray("tag")                 // repeated parameters
    name  := c.PostForm("name")                  // form body
    file, _ := c.FormFile("avatar")              // multipart upload
    auth  := c.GetHeader("Authorization")
    ip    := c.ClientIP()                        // see Security defaults
    route := c.FullPath()                        // "/users/:id" — log this, not the URL

    // A body that failed to parse is not the same as a field that was absent.
    if err := c.FormError(); err != nil {
        c.AbortWithStatusJSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
        return
    }

    // Values passed down the chain
    c.Set("requestID", id)
    who := c.GetString("user")                   // typed getters for the common cases

    // Response
    c.JSON(http.StatusOK, payload)
    c.Text(http.StatusOK, "plain text")          // Text, not String, for a variable
    c.Data(http.StatusOK, "text/csv", body)
    c.Header("X-Trace", "abc")
    c.Redirect(http.StatusFound, "/elsewhere")   // 300-308 only
}
```

Two conventions worth internalising:

- **`c.Text(code, s)` for text from a variable.** `c.String` treats its first argument as a format
  template, so `go vet` flags it and the `fmt` path costs an allocation.
- **`c.FullPath()` in logs and metrics**, never the raw URL. It is the route pattern, so it has
  bounded cardinality and cannot carry a customer id.

### Background work

A `Context` returns to the pool the moment the handler does. Anything outliving the handler needs
a copy — and the body has to be read before you leave:

```go
func handler(c *zarp.Context) {
    body, _ := io.ReadAll(c.Request.Body)  // read it here
    cp := c.Copy()                         // params and keys, detached
    go audit(cp, body)                     // not c, and not cp.Request.Body
    c.Status(http.StatusAccepted)
}
```

---

## 🔗 Middleware

Middleware and handlers are the same type. What makes one middleware is that it calls `c.Next()`.

```go
func timing(c *zarp.Context) {
    start := time.Now()
    c.Next()                                    // run the rest of the chain
    log.Printf("%s took %s", c.FullPath(), time.Since(start))
}

r.Use(timing)                                   // global
api := r.Group("/api", timing)                  // per group
r.GET("/one", timing, handler)                  // per route
```

> ⚠️ **Returning does not stop the chain.** The chain is a slice walked by a cursor, so a handler
> that returns simply lets the loop advance — which is what lets a route handler omit `Next()`.
> Middleware that writes a 401 and returns **without** `c.Abort()` will still run the route
> handler. Use `c.Abort()`, or one of `c.AbortWithStatus` / `c.AbortWithStatusJSON`.

### Included

```go
r.Use(middleware.Default()...)                     // Logger + Recovery
r.Use(middleware.Recovery())                       // panic → 500, stack to stderr
r.Use(middleware.Logger())                         // aligned text to stderr
r.Use(middleware.MaxBodySize(8 << 20))             // cap every body, not just bound ones
r.Use(middleware.Timeout(5 * time.Second))         // deadline on the request context
r.Use(middleware.RequestID())                      // generated id, echoed in the response
r.Use(middleware.CORS())                           // any origin, no credentials
```

Each has a `WithConfig` form:

```go
r.Use(middleware.CORSWithConfig(middleware.CORSConfig{
    AllowOrigins:     []string{"https://app.example.com"},
    AllowCredentials: true,
    MaxAge:           12 * time.Hour,
}))

r.Use(middleware.RequestIDWithConfig(middleware.RequestIDConfig{
    TrustInbound: true,   // only behind a gateway that sets the header itself
}))
```

### Logging through your own logger

The logger depends on no logging library. Any logger plugs in through `Sink`, one method:

```go
r.Use(middleware.LoggerWith(middleware.SinkFunc(func(e middleware.Entry) {
    // Method, Path (the route pattern), URL, Status, Latency, Size, ClientIP,
    // and Level(), which maps status to severity.
})))

r.Use(middleware.LoggerWith(middleware.SlogSink(slog.Default())))
```

`middleware.SlogSink` ships for `log/slog`. For a structured logger faster on this path,
[loggy](https://github.com/subhanjanOps/loggy) is worth a look — the adapter is about ten lines,
and the shape is documented on `Sink`.

---

## 📥 Binding & validation

`binding/` is an optional import: a program that decodes its own bodies pays nothing for it — in
particular nothing for the reflection the form binders use.

**Binding and validation are separate steps**, so a malformed body and an unacceptable value get
different answers.

```go
type CreateUser struct {
    Name  string `json:"name"  binding:"required,min=2,max=64"`
    Email string `json:"email" binding:"required,email"`
    Role  string `json:"role"  binding:"required,oneof=admin member"`
}

func createUser(c *zarp.Context) {
    var in CreateUser

    if err := binding.JSON(c, &in); err != nil {   // parse only
        bindingFailed(c, err)
        return
    }
    if err := binding.Validate(in); err != nil {   // rules only
        bindingFailed(c, err)
        return
    }
    // ... or binding.BindAndValidate(c, &in) for both, picking the binder
    // from the request's method and Content-Type.
}
```

Mapping failures onto the statuses that mean them:

```go
func bindingFailed(c *zarp.Context, err error) {
    var invalid binding.ValidationErrors
    switch {
    case errors.Is(err, binding.ErrUnsupportedMediaType):
        c.AbortWithStatusJSON(http.StatusUnsupportedMediaType, ...)   // 415
    case errors.As(err, &invalid):
        c.AbortWithStatusJSON(http.StatusUnprocessableEntity, fields(invalid)) // 422
    default:
        c.AbortWithStatusJSON(http.StatusBadRequest, ...)             // 400
    }
}
```

`ValidationErrors` reports **every** failing field, not just the first, and names each field as the
request did — the `json`/`form`/`uri`/`header` tag, never the Go identifier.

| Binder | Source | Tag |
|---|---|---|
| `binding.JSON` | request body | `json` |
| `binding.Query` | URL query string | `form` |
| `binding.Form` | urlencoded body | `form` |
| `binding.Multipart` | multipart values | `form` |
| `binding.URI` | matched route parameters | `uri` |
| `binding.Header` | request headers | `header` |

Rules: `required`, `min`, `max`, `len`, `oneof`, `email`. Deliberately small — anything more
specific belongs in the handler, where it can say what it means.

Per-route JSON settings, without touching anything global:

```go
var uploads = binding.JSONWith(binding.JSONConfig{
    MaxBodyBytes:          50 << 20,   // default is 4 MB
    DisallowUnknownFields: true,
})

if err := binding.With(c, &req, uploads); err != nil { ... }
```

---

## 📤 Rendering

`render/` is the other optional import — JSON, XML, HTML, text, and redirects. Every renderer
builds its output before writing a byte, so a failure half way through cannot leave a committed
status on a broken body.

```go
render.JSON(c, http.StatusOK, payload)
render.IndentedJSON(c, http.StatusOK, payload)
render.XML(c, http.StatusOK, payload)
render.Text(c, http.StatusOK, "hello")
render.Data(c, http.StatusOK, "text/csv", body)
render.Redirect(c, http.StatusFound, "/elsewhere")

// HTML from a loaded template set
tpl, err := render.LoadGlob("templates/*.html")
tpl.HTML(c, http.StatusOK, "index.html", data)
```

`c.JSON` in core and `render.JSON` produce identical bytes — a test asserts it, so the two cannot
drift. Core keeps its own writer because it is cheaper; `render` exists for the extensible path.

---

## 📊 Benchmarks

Full numbers, methodology and the reproduction commands are in
[benchmarks/BASELINE.md](benchmarks/BASELINE.md). `go1.25.0 windows/amd64`, AMD Ryzen 7 7435HS,
`-count=5`, averaged.

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

**Read that second table fairly.** `ServeMux.Handler()` does more than match a tree — it cleans the
path, matches hosts, and stores wildcards in the request context, which is where most of its
allocations come from. zarp's tree walk resolves the route into a caller-supplied buffer and leaves
the rest to `Engine`. Read it as *route resolution is allocation-free and roughly an order of
magnitude cheaper*, not as a claim about whole requests. The end-to-end table is the one to judge
zarp by.

**Middleware and context:**

| | ns/op | allocs |
|---|---|---|
| no middleware | 51.9 | 0 |
| chain of 3 | 57.7 | 0 |
| chain of 10 | 78.4 | 0 |
| `Recovery` | 59.5 | 0 |
| `Logger` (text → `io.Discard`) | 356.5 | 0 |
| context `reset` | 3.9 | 0 |
| `Param` | 4.7 | 0 |
| `Query` (cached) | 13.1 | 0 |

**~2.8 ns per middleware hop**, allocation-free — an index cursor rather than a closure and a stack
frame per layer.

**What "allocation-free" covers.** Routing, the context pool, the middleware chain, param lookup
and writing a response: the paths above. It is not a claim about every method on `Context`.
`Query()` and `Set()` each allocate a map the first time they are called on a request, and are free
afterwards. `binding/` and `render/` allocate by design, which is why they are optional imports and
why their costs are listed separately in the baseline. In core, 405 responses are the one path that
allocates — they walk the other method trees to build `Allow`, which is why
`HandleMethodNotAllowed` is off by default.

```sh
go test -bench=. -benchmem -run XXX -count=5 ./benchmarks/       # end to end
go test -bench='Router|ServeMux|Context' -benchmem -run XXX .    # micro-benchmarks
```

---

## 🔒 Security defaults

Defaults are a security posture, so zarp's are the conservative ones. Each is reversible
deliberately, and none is reversed for you.

| Default | Why |
|---|---|
| `ClientIP()` returns `RemoteAddr`; `X-Forwarded-For` and `X-Real-Ip` are ignored | Those headers are written by whoever connects. Set `ForwardedByClientIP` and list your `TrustedProxies` — the chain is then walked from the right, past hops you own, so a client cannot forge one |
| `RequestID` generates its own id and ignores an inbound one | An inbound id is an attacker-chosen string that lands in every log line for the request. Set `TrustInbound` behind a gateway that sets the header itself |
| JSON binding caps the body at 4 MB and rejects a second document after the first | An uncapped decode is a memory-exhaustion vector, and `{"a":1} {"b":2}` is a request two parsers can disagree about |
| An unrecognised `Content-Type` fails with `ErrUnsupportedMediaType` | Guessing JSON turns a 415 into a confusing 400 |
| `CORS` rejects wildcard-plus-credentials, and malformed origins, at construction | Both are configuration that cannot do what it appears to |
| `HandleMethodNotAllowed` is off | It probes every other method tree on a miss, and allocates to build `Allow` |
| Logged paths have their control characters escaped | On a route miss the logged path is the client's URL, decoded — a `%0A` in it would otherwise forge log lines |

Behind a proxy:

```go
r.ForwardedByClientIP = true
r.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
```

`middleware.MaxBodySize` bounds every body, not only the ones a binder reads, and
`middleware.Timeout` puts a deadline on the request context.

---

## ♻️ Lifecycle: configure, then serve

An `Engine` is built, then read. Registering routes, adding middleware, setting fallbacks and
assigning the configuration fields all mutate state that `ServeHTTP` reads **without a lock** —
deliberately, because a mutex on every lookup would cost every request to support something almost
no program does.

```go
r := zarp.New()
r.ForwardedByClientIP = true          // configuration
r.Use(middleware.Recovery())          // middleware
r.GET("/users/:id", show)             // routes
http.ListenAndServe(":8080", r)       // and only now, requests
```

Changing any of it while requests are in flight is a data race, and the race detector will say so.

---

## 🔬 How it works

### The radix tree

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

**Catch-alls are three nodes**, not one: the static parent ending *before* the slash, an empty-path
intermediate, and the leaf holding `/*name`. The intermediate is what lets `/src/` itself match,
with the slash landing inside the wildcard value rather than being eaten by the parent's prefix.

### The context

One `Context` is pooled per in-flight request. `sync.Pool` gives a per-P free list, so under load a
`Get` is usually a pointer bump with no lock and no allocation. Two rules keep that correct:

- **`reset` clears every field.** A field left behind is a cross-request data leak — one user
  seeing another's values. A reflection-based test walks the struct and fails when a field is added
  without a matching `reset` line.
- **The `*Context` must not escape the handler.** Once it returns to the pool another goroutine can
  own it; a handler spawning one must `Copy()` what it needs.

The response writer is embedded **by value**, so it pools with the context instead of costing a
second allocation. It records status and size for middleware to log, and defers the actual
`WriteHeader` call so a later handler in the chain can still change the status.

### The middleware chain

Middleware is a `[]HandlerFunc` walked by an index cursor on the context:

```go
c.index++
for int(c.index) < len(c.handlers) {
    c.handlers[c.index](c)
    c.index++
}
```

A middleware wanting before/after behaviour calls `c.Next()` in its middle; the loop resumes when
the nested call returns, because the index is shared state. One wanting to stop the chain calls
`c.Abort()`, which sets the index above any legal chain length so the loop condition fails at every
level of the stack at once. Cost per layer: one integer increment. The
`func(http.Handler) http.Handler` alternative adds a stack frame to every request instead.

---

## 🤔 Why another router

Ergonomics are usually paid for on every request: a `map[string]string` of route params, a closure
allocated per middleware, a reflection call to render JSON. Each cost is small on its own; together
they are what separates a routing layer's throughput from the standard library's.

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

## 🗂️ Repository layout

```
zarp/
├── doc.go                     package documentation
├── router.go                  radix tree: node, addRoute, Lookup
├── context.go                 Context, pooling, params, query/form, response helpers
├── engine.go                  Engine, sync.Pool wiring, ServeHTTP, 404/405/redirects
├── group.go                   RouterGroup, verbs, prefixes, Use, IRouter
├── handler.go                 HandlerFunc, Next, Abort, NotFound
├── static.go                  Static, StaticFS, StaticFile, SecureDir
├── internal/bytesconv/        unsafe []byte↔string helpers, module-private
├── binding/                   request parsing and validation, optional import
├── render/                    JSON/XML/HTML/text rendering, optional import
├── middleware/                logger, recovery, cors, requestid, maxbody, timeout
├── examples/                  five runnable apps
├── benchmarks/                end-to-end perf suite plus BASELINE.md
├── CHANGELOG/                 one file per release
├── ROADMAP.md                 what is built and what comes next
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
gosec ./...                                        # CI gates this at 0 issues

go test ./...                                      # unit tests; benchmarks/ has none
go test -run TestAddRouteLookup .                  # a single test
go test -race ./...                                # see the Windows note below
go test -cover . ./binding ./render ./middleware   # CI gates this at 90%

go test -bench=. -benchmem ./benchmarks/...
go test -bench=BenchmarkRouterParam -benchmem -count=10 -run XXX . > new.txt
benchstat old.txt new.txt                          # required for perf-sensitive changes
```

On Windows, ThreadSanitizer often fails to start (`ThreadSanitizer failed to allocate ... error
code: 87`). Run the race detector under Linux instead:

```sh
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W)":/src -w /src golang:1.25 go test -race ./...
```

---

## 🤝 Contributing

Bug reports and pull requests are welcome. [CONTRIBUTING.md](CONTRIBUTING.md) covers the design
constraints a change is held to, the `benchstat` comparison required for anything touching the
router, context or middleware chain, and the pre-PR checklist.

Please open an issue first for anything that changes the public API, adds a package, or touches the
router.

---

## 📄 License

MIT — see [LICENSE](LICENSE).
