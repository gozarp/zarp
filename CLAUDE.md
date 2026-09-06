# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

# gomicro

A lightweight Go REST framework. Gin-style ergonomics, stdlib-level performance overhead.

## Current state

The module is scaffolding: `context.go` holds a stub `Context` embedding `context.Context`, `engine.go` is package declaration only, and `binding/`, `render/`, `middleware/`, `internal/`, `examples/`, `benchmarks/` are empty directories. Nothing under "Core API shape" is implemented yet. Follow the build order below — `router.go` does not exist and is step 1.

Module path is `github.com/subhanjanops/gomicro`, Go 1.25.0. No third-party dependencies; keep it stdlib-only in the core package.

## Commands

```sh
go build ./...
go vet ./...
gofmt -l .                                   # should print nothing

go test ./...                                # unit tests; excludes benchmarks by design
go test -run TestRouterStatic ./...          # a single test by name
go test -run 'TestRouter' -v .               # root package only, verbose

go test -bench=. -benchmem ./benchmarks/...  # benchmarks, run deliberately
go test -bench=BenchmarkRouterParam -benchmem -count=10 ./benchmarks/... > new.txt
benchstat old.txt new.txt                    # required for perf-sensitive changes

go test -race ./...
```

Empty directories contain no Go files yet, so `./...` currently matches only the root package.

## Design goals (non-negotiable)

- **Radix tree routing** — not regex, not linear scan. O(path length) matching per HTTP method.
- **`sync.Pool` for `Context`** — zero per-request allocation for the context object.
- **Route params as `[]Param{Key, Value}`** — never `map[string]string`. Maps are the #1 GC pressure source in naive routers; avoid them in the hot path entirely.
- **Middleware as index-cursor chain** — `[]HandlerFunc` executed via `c.Next()` incrementing an index. Not nested closures (`func(http.Handler) http.Handler`), which add a call-stack frame per middleware.
- **No reflection in the hot path** — JSON binding uses `encoding/json` directly. Struct-tag validation is opt-in, lives in `binding/validator.go`, never imported by core.
- **No built-in rendering engine in core** — HTML templates, XML, etc. are optional add-on packages, not core dependencies.

Every PR or generated change should be checked against this list before merging. If something adds an allocation, a reflection call, or a map lookup to the hot request path, it needs explicit justification in the PR description.

## Folder structure

```
gomicro/
├── context.go        # Context struct, sync.Pool, Next(), param access
├── engine.go          # Engine struct, ServeHTTP, method registration (GET/POST/...)
├── group.go           # RouterGroup, prefix + middleware nesting
├── router.go          # radix tree: node, insert, match
├── handler.go          # HandlerFunc type, handler chain execution
├── binding/           # request parsing/validation (optional import)
├── render/             # response rendering (optional import)
├── middleware/         # builtin middlewares (logger, recovery, cors, requestid)
├── internal/bytesconv/ # unsafe []byte<->string helpers, not exported outside module
├── examples/           # runnable example apps
├── benchmarks/          # perf benchmarks, run explicitly via -bench, not part of go test ./...
└── *_test.go            # unit tests colocated with source at root
```

## Core API shape

```go
type HandlerFunc func(*Context)

type Engine struct {
    trees   map[string]*node // method -> radix tree
    pool    sync.Pool
    noRoute []HandlerFunc
}

func (e *Engine) GET(path string, handlers ...HandlerFunc)
func (e *Engine) Group(prefix string, mw ...HandlerFunc) *RouterGroup
func (c *Context) Next()
func (c *Context) JSON(code int, obj any)
```

## Build order

1. `router.go` — radix tree first. This determines whether we hit stdlib-level perf. Every change here needs a companion benchmark against `net/http.ServeMux`.
2. `context.go` — `sync.Pool` wiring, param access, response helpers.
3. `engine.go` — ties router + context + middleware chain together.
4. `group.go`, `handler.go` — nesting and chain execution.
5. `binding/`, `render/`, `middleware/` — optional packages, built after core is stable.

Don't jump ahead of this order unless explicitly asked to.

## Testing conventions

- Unit tests colocated with source (`router_test.go` next to `router.go`), not in a separate `tests/` dir.
- `benchmarks/` is separate so `go test ./...` stays fast; benchmarks run deliberately via `go test -bench=. ./benchmarks/...`.
- Router correctness tests must cover: static routes, `:param` routes, conflicting routes, `*catchall`, trailing slash behavior.
- Any perf-sensitive change (context, router, middleware chain) needs a `benchstat` comparison before/after in the PR description.

## Style notes

- Root package (`gomicro`) stays flat — no subpackage for core files, mirrors Gin/chi.
- `internal/` blocks external imports of unsafe/perf-hack helpers — don't move things out of `internal/` without a clear reason.
- Prefer explicit allocation-free code over "clever" abstractions in `context.go` and `router.go` specifically. Elsewhere, normal Go idioms are fine.