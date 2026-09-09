# v0.1.0

**Released 2026-09-09** — the first tagged release of zarp.

Requires Go 1.25 or later. No third-party dependencies in any package.

```sh
go get github.com/gozarp/zarp@v0.1.0
```

This is a `v0.x` tag and the API is not frozen. What it does promise is that the version you build
against stops moving under you, which until now was not true — `go get` resolved to whatever commit
was on `main`.

---

## What's in it

### Core (`zarp`)

- **Radix-tree router**, one tree per HTTP method, matching in O(path length). Node children are
  selected through an `indices` byte string rather than a map. Supports static segments, `:param`
  segments and `*catchall`, with conflict detection at registration time.
- **Pooled `Context`** backed by `sync.Pool`, with route params carried as a `[]Param` slice rather
  than a map. A request that routes, runs its chain and writes a response allocates nothing.
- **`Engine`** with 404/405 handling, automatic trailing-slash redirects, `NoRoute`/`NoMethod`
  fallbacks, and `Run`/`RunTLS` helpers.
- **Route groups** with inherited prefixes and middleware, behind the `IRouter`/`IRoutes`
  interfaces.
- **Middleware chain** driven by an index cursor and `c.Next()`, not nested closures — one function
  call per middleware rather than one stack frame.
- **Static file serving** via `Static`, `StaticFS` and `StaticFile`.
- **`SecureDir`** — an `http.FileSystem` that refuses any path resolving outside its root.
- **`ResponseWriter`** wrapping `http.ResponseWriter` with deferred status recording, byte counting,
  and an `Unwrap` method that makes `http.NewResponseController` work through the wrapper.

### Request binding (`binding`)

JSON, form, multipart, query, URI and header binding into structs, plus struct-tag validation.
Parsing and validation are separate calls — `JSON` parses, `Validate` checks, `BindAndValidate`
does both — so a handler can distinguish a body it could not parse from one it will not accept.
Per-route decoder configuration comes from `JSONWith(cfg)`.

### Rendering (`render`)

JSON, indented JSON, XML, text, raw data, redirects and HTML templates, behind a `Render`
interface. An optional import; core never depends on it.

### Middleware (`middleware`)

`Logger` (with text, `slog` and custom sinks), `Recovery`, `CORS`, `RequestID`, `MaxBodySize` and
`Timeout`.

---

## Security defaults

The recurring theme of the pre-tag work: in several places the safe configuration existed but was
not the default. These are the ones that changed.

- **Proxy headers are not trusted by default.** `ForwardedByClientIP` is off, and `TrustedProxies
  []netip.Prefix` was added. When it is on, `X-Forwarded-For` is walked right-to-left, skipping
  trusted hops. Previously `ClientIP()` returned the leftmost entry — fully attacker-controlled on
  any directly-reachable server, which poisons rate limits, allowlists and audit logs.
- **Inbound request IDs are not trusted by default.** `RequestID`'s `TrustInbound` is off, and the
  accepted grammar is `[A-Za-z0-9._:-]{1,128}`. Header injection was already blocked, but the
  previous printable-ASCII rule allowed quotes and spaces, which confuse structured logs.
- **Malformed configuration panics at construction** rather than silently doing nothing.
  `CORSWithConfig` validates origins — `https://example.com/` and `https://example.com` are
  different strings and only one ever matches — and requires them lowercase, scheme included.
  `RequestIDWithConfig` rejects a malformed header name and validates its generator's output.
  `MaxBodySize` panics on a limit of zero or less, which `net/http` reads as "no bytes at all".
- **`SecureDir` closes the symlink escape** that `http.Dir` leaves open. `http.Dir` blocks `../` but
  follows symlinks out of the tree, and `r.Static("/assets", "./public")` reads like a filesystem
  boundary — now it can be one.
- **Body size limits moved before the route.** `MaxBodySize` middleware replaces a limit that lived
  inside the JSON binder and so applied only to handlers that happened to use it.
- **Log forging is closed.** The text sink wrote `Entry.Path` raw, and on a route miss that field is
  the requested path, which `net/http` hands over URL-decoded — so `%0A` in a request line became a
  real newline and let a caller write their own log entries. Control characters are now escaped on
  the way out, without allocating. `Recovery` had the same hole and also escapes the panic value.

## Request parsing correctness

- **A request body carries exactly one JSON document.** `{"a":1} {"b":2}` used to bind
  successfully; trailing content is now `ErrTrailingContent`. Detection uses a second `Decode`
  requiring `io.EOF` rather than `Decoder.More`, which answers false on the tokens that close an
  object or array and so accepted `{"a":1} }`.
- **An unsupported `Content-Type` returns `ErrUnsupportedMediaType`** instead of falling back to the
  JSON decoder. The four body outcomes — malformed, unsupported, missing, too large — are now
  distinguishable.
- **Media types match case-insensitively,** per RFC 9110 §8.3.1, both in `binding.Default` and one
  layer down in `Context.initFormCache`. `Application/JSON` was answered with 415; worse, a
  `Multipart/Form-Data` body skipped the multipart parse and every field came back empty — a silent
  wrong answer rather than an error.
- **`Context.FormError`** was added, so a body that failed to parse is distinguishable from one that
  omitted every field.
- **`FieldError.Field` resolves from the `json`/`form`/`uri`/`header` tag** before falling back to
  the Go field name, so an API error names `user_name` rather than leaking `UserName`.
- **`min`/`max`/`len` are parsed per field kind** (int64 / uint64 / float64) rather than through
  `float64`, which cannot represent integers past 2^53 exactly.
- **`Redirect` accepts 300–308 only,** in both `Context` and `render`, which previously disagreed.
  A created resource wants `Location` plus a body — `c.Header` and `c.JSON`.
- **`isValidMethod` accepts an RFC 9110 token** minus lowercase. A-Z-only made IANA-registered
  methods like `BASELINE-CONTROL` and `M-SEARCH` unregisterable. Lowercase stays rejected: methods
  are case-sensitive, so `"get"` builds a route no client reaches.

## API shape

- **Package-level mutable binder variables are gone.** `binding.JSONBinding` and friends are now
  functions returning shared immutable binders. As variables they were process-wide mutable state —
  one import reassigning `binding.JSONBinding` changed decoding for the whole program, and doing so
  after serving started was a data race.
- **The `MaxBodyBytes` / `EnableDecoder*` globals were replaced** by `JSONConfig` and
  `JSONWith(cfg)`, for the same reasons plus testability under `t.Parallel()`.
- **Binding no longer validates behind your back.** The binder functions bind; `BindAndValidate`
  does both.
- **`Context.NotFound()`** was added, so `static.go` no longer reaches into `c.handlers` and
  `c.index` to trigger a fallback.
- **`middleware.Timeout` and `middleware.MaxBodySize`** were added.
- **Load-bearing interfaces are asserted at compile time** — `*Engine`/`http.Handler`,
  `*Context`/`context.Context`, `*responseWriter`/`ResponseWriter`, and every renderer and binder.

## Documented contracts

Four behaviours that look like bugs until you know why, now written down at each site:

- Returning from a handler does **not** stop the chain; only `Abort` does.
- `WriteHeader` records a status without sending it; `WriteHeaderNow`, the first write, or a flush
  sends it.
- `Copy()` detaches metadata but not `Request.Body`, which is closed when the handler returns — and
  it carries the request's context, which `net/http` cancels when the response finishes.
- An `Engine` is configured, then served. Route registration and config fields are read without a
  lock by `ServeHTTP`; mutating either while serving is a data race.

Also documented: `Entry.URL` carries whatever the client sent, query string included, which is
where tokens and PII end up.

## Fixed

- **`Context.NotFound` recursed forever** when called from inside a `NoRoute` handler, and produced
  an empty body where the router's own 404 produces a default one. It now routes through the same
  `serveFallback` the router uses and detects that it is already on the fallback chain.
- **Body limits were not reaching `net/http`.** `http.MaxBytesReader` signals an oversized request
  through a method only `net/http`'s own response writer has, and the wrapper did not forward it, so
  an oversized body was drained rather than cut off. Fixed by `ResponseWriter.Unwrap`.
- **The logger's pooled line buffer is capped at 4 KB.** `Entry.Path` is the client's URL on a route
  miss, bounded only by `MaxHeaderBytes`, so one such request could leave a megabyte-wide buffer in
  the pool for the life of the process, per P. The JSON buffer pool already had this guard.

---

## Verification

Run against the tagged tree:

| Check | Result |
|---|---|
| `go build`, `go vet`, `gofmt -l` | clean |
| `golangci-lint run ./...` | 0 issues |
| `go test ./...` | 283 tests, all passing |
| `go test -race ./...` | clean, run under Linux (Windows ThreadSanitizer cannot start) |
| Coverage — `.` / `binding` / `render` / `middleware` | 96.2% / 96.7% / 100% / 94.3%, against a 90% gate |
| `gosec ./...` | 0 issues, 2 documented `#nosec` annotations on `Engine.Run` |
| `govulncheck ./...` | no findings on a current Go 1.25 patch release |

`benchstat` over 8 runs was last taken against the pre-hardening tree during development, not
re-run at the tag: no significant change on any benchmark (p > 0.05), allocs/op unchanged at 0.
`benchmarks/BASELINE.md` is the checked-in reference.

`govulncheck` reports standard-library findings when run on a toolchain older than **go1.25.3** —
they are fixed in go1.25.2 and go1.25.3, and reachable through `Engine.Run`. Keep your toolchain
patched; CI resolves `go-version: '1.25'` to the newest patch for this reason.

---

## Known limitations

- **The API is not frozen.** Renaming or resigning an exported symbol is still allowed before
  `v1.0.0`, though it should be a considered change.
- **`Engine.Run` starts a server without timeouts,** deliberately and documented — it is a
  convenience for development. Production deployments should construct their own `http.Server`.
  This is what the two `#nosec` annotations cover.
- **The router is not synchronised.** Register routes and set config before serving; see the
  configure-then-serve contract above.
- **No comparative benchmarks** against other frameworks. Importing them would break the
  zero-dependency invariant, so it belongs in a separate repository.
