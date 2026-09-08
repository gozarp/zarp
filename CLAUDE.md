# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

# zarp

A lightweight Go REST framework: expressive routing and middleware at stdlib-level overhead.

Module path is `github.com/gozarp/zarp`, Go 1.25.0. No third-party dependencies in any package —
CI asserts this, so adding one is a deliberate, breaking decision, not a convenience.

## Current state

The framework is complete and tested: radix router, pooled `Context`, `Engine`, route groups,
middleware chain, static file serving, plus the optional `binding/`, `render/` and `middleware/`
packages. 229 tests, clean under `-race`, allocation-free end to end.

Pre-release: the API is not frozen and nothing is tagged. Renaming or resigning an exported symbol
is still allowed, but it should be a considered change, not a drive-by.

## Commands

```sh
go build ./...
go vet ./...
gofmt -l .                                         # should print nothing
golangci-lint run ./...                            # config in .golangci.yml

go test ./...                                      # unit tests; benchmarks/ has none
go test -run TestAddRouteLookup .                  # a single test by name
go test -run 'TestRouter' -v .                     # root package only, verbose
go test -race ./...                                # see the Windows note below
go test -cover . ./binding ./render ./middleware   # CI gates this at 90%

go test -bench=. -benchmem ./benchmarks/...        # end-to-end suite, run deliberately
go test -bench=. -benchmem -run XXX .              # router and context micro-benchmarks
go test -bench=BenchmarkRouterParam -benchmem -count=10 -run XXX . > new.txt
benchstat old.txt new.txt                          # required for perf-sensitive changes
```

On Windows, ThreadSanitizer often fails to start (`ThreadSanitizer failed to allocate ... error
code: 87`). Run the race detector under Linux instead; CI's race job is the authoritative one:

```sh
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W)":/src -w /src golang:1.25 go test -race ./...
```

## Design constraints (non-negotiable)

- **Radix tree routing** — not regex, not linear scan. O(path length) matching per HTTP method.
  Node children are picked through the `indices` byte string, never a map.
- **`sync.Pool` for `Context`** — zero per-request allocation for the context object. The response
  writer is embedded by value so it pools with the context.
- **Route params as `[]Param{Key, Value}`** — never `map[string]string`. Maps are the largest GC
  pressure source in naive routers; keep them out of the hot path entirely.
- **Middleware as index-cursor chain** — `[]HandlerFunc` executed via `c.Next()` incrementing an
  index. Not nested closures (`func(http.Handler) http.Handler`), which add a call-stack frame per
  middleware.
- **No reflection in the hot path** — core JSON uses `encoding/json` directly. Struct-tag binding
  and validation are opt-in, live in `binding/`, and are never imported by core.
- **No rendering engine in core** — HTML templates, XML and friends live in `render/`, an optional
  import, not a core dependency.

Check every change against this list. If something adds an allocation, a reflection call, or a map
lookup to the hot request path, it needs an explicit justification in the PR description.

## Invariants CI enforces

Three of these are asserted by the workflow rather than left to review:

- **Core has no non-stdlib dependency**, and the module as a whole has no third-party requirement.
- **Core imports none of `binding/`, `render/`, `middleware/`.** The optional packages depend on
  core, never the reverse.
- **Coverage over `.`, `binding/`, `render/`, `middleware/` stays at or above 90%.**

Two more are asserted by tests:

- **`Context.reset` clears every field.** A field left behind is a cross-request data leak. A
  reflection-based test walks the struct and fails when a field is added without a matching
  `reset` line — if that test fails, add the line rather than amending the test.
- **`render.JSON` and `Context.JSON` produce identical bytes.** Core keeps its own direct writer
  instead of routing through `Render`, and the test is what stops the two from drifting.

## Repository layout

```
zarp/
├── doc.go              package documentation
├── router.go           radix tree: node, addRoute, Lookup
├── context.go          Context, pooling, params, query/form, response helpers
├── engine.go           Engine, sync.Pool wiring, ServeHTTP, 404/405/redirects
├── group.go            RouterGroup, verbs, prefixes, Use, IRouter/IRoutes
├── handler.go          HandlerFunc, Next, Abort
├── static.go           Static, StaticFS, StaticFile
├── internal/bytesconv/ unsafe []byte<->string helpers, not exported outside the module
├── binding/            request parsing and validation (optional import)
├── render/             response rendering (optional import)
├── middleware/         logger, recovery, cors, requestid
├── examples/           runnable example apps
├── benchmarks/         end-to-end perf suite plus BASELINE.md, run explicitly via -bench
└── *_test.go           unit tests colocated with source at root
```

## Testing conventions

- Unit tests are colocated with source (`router_test.go` next to `router.go`), not in a separate
  `tests/` directory.
- `benchmarks/` is separate so `go test ./...` stays fast; it holds no `Test` functions and runs
  deliberately via `-bench`.
- Router correctness tests must cover static routes, `:param` routes, conflicting routes,
  `*catchall`, and trailing-slash behaviour.
- `benchmarks/BASELINE.md` is the checked-in reference. Update it in the same PR as whatever moved
  the numbers, with the reason in the commit message.
- Any perf-sensitive change (router, context, middleware chain) needs a `benchstat` comparison in
  the PR description. The number that matters is allocs/op — ns/op moves with the machine, but an
  allocation appearing where there was none is a design regression.

## Style notes

- The root package (`zarp`) stays flat — no subpackage for core files.
- `internal/` blocks external imports of the unsafe/perf-hack helpers; don't move things out of it
  without a clear reason.
- Prefer explicit allocation-free code over "clever" abstractions in `context.go` and `router.go`
  specifically. Elsewhere, normal Go idioms are fine.
- Exported symbols carry doc comments; `revive`'s `exported` rule is on in `.golangci.yml`.
- Ignoring an error is written `_, _ = w.Write(b)`, not left bare — the intent should be visible,
  and `errcheck` is on.
