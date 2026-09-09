# Roadmap

Where zarp is today, and where it goes next.

The short version: the framework is finished for what it set out to do. Routing, context, the
middleware chain, binding, rendering and the middleware set are all built, tested and measured, and
`v0.1.0` is the first tag. What is left is not features — it is the slower work of keeping the API
honest while people actually start using it.

For the full record of what shipped in `v0.1.0` and why, see
[`CHANGELOG/CHANGELOG-0.1.0.md`](CHANGELOG/CHANGELOG-0.1.0.md).

---

## What is implemented

Everything here is in `v0.1.0`, covered by tests, and clean under `-race`.

**The core** (`zarp`) is a radix-tree router with one tree per HTTP method, a `sync.Pool`-backed
`Context` that allocates nothing per request, route groups with inherited prefixes and middleware,
and an index-cursor middleware chain driven by `c.Next()`. Static file serving is built in, and
`SecureDir` gives you an `http.FileSystem` that refuses to serve anything resolving outside its
root — symlinks included, which plain `http.Dir` will happily follow.

**Request binding** (`binding`) parses JSON, form, multipart, query, URI and header data into
structs. Parsing and validation are deliberately separate calls, so a handler can answer 400 for a
body it could not parse and 422 for one it could parse but will not accept. Per-route decoder
settings come from `JSONWith(cfg)`; there is no process-wide global to reassign.

**Rendering** (`render`) covers JSON, indented JSON, XML, text, raw data, redirects and HTML
templates. It is an optional import — core never depends on it.

**Middleware** (`middleware`) ships logger, recovery, CORS, request ID, body-size limits and
timeouts. The defaults are the safe ones: proxy headers are not trusted, inbound request IDs are
not trusted, and a malformed CORS or request-ID config panics at construction rather than silently
doing nothing.

The measured numbers, as of the tag: 283 tests passing, 96% coverage across the four library
packages against a 90% CI gate, zero allocations on the routing and response paths the benchmarks
cover, and zero third-party dependencies in any package — asserted by CI rather than by convention.

Getting there took four review passes and a design-pattern audit. Most of that work went into
defaults and signatures rather than features, on the theory that those are the things which become
expensive to change once people depend on them.

---

## What comes next

Nothing below is scheduled. The ordering reflects what seems most useful, not a commitment.

### Before `v1.0.0` — earning the API freeze

The API is not frozen, and a `v1.0.0` tag is a promise not to break it. That promise should be
bought with real usage, not with confidence.

- **Let the API sit.** The most valuable thing between now and `v1.0.0` is people building things
  and finding the parts that are awkward. Breaking changes are still cheap; after `v1.0.0` they
  are not. This is the reason to wait, and it is a better reason than any feature.
- **A comparative benchmark suite** against gin, echo, chi and httprouter. Worth having and
  currently impossible here — importing four frameworks would break the zero-dependency invariant
  CI enforces. It belongs in a separate repository that imports zarp, not in this module.
- **Fuzz the router and the binders.** Path matching and body parsing are the two places where a
  malformed input should produce a clean error and never a panic. `go test -fuzz` is the right
  tool and has not been pointed at either yet.
- **An interoperability pass** over `http.Handler`, `http.HandlerFunc` and the wider middleware
  ecosystem. `ResponseWriter.Unwrap` already makes `http.NewResponseController` work through the
  wrapper; whether the adapters around that are as pleasant as they should be is untested by
  anything but the examples.

### Optional packages, if the demand is real

Each of these is reasonable software and none of it belongs in a core whose entire proposition is
that it stays small. If any lands, it lands as a separate import you opt into, the way `binding`
and `render` already do.

- **Rate limiting** — a token bucket keyed by `ClientIP()`, which now that proxy handling is
  trustworthy is finally a key worth rate-limiting on.
- **Content negotiation** — an `Accept`-header-driven counterpart to the binding side.
- **OpenAPI generation** from route registrations and binding tags.
- **Typed query and param helpers** — `c.QueryInt("page")` and friends, which every project using
  this framework will otherwise write for itself.
- **A structured `HTTPError` type** so handlers can return errors that carry a status.

### Things worth doing whenever

- **More examples.** The five in `examples/` cover the basics. Authentication, file upload, and
  graceful shutdown behind a real proxy are the ones people ask for.
- **Documentation on pkg.go.dev.** Now that there is a tag, the reference docs have a version to
  render against, and the doc comments should be read as a whole rather than in isolation.

---

## Deliberately not doing

These have all been proposed, checked against the source, and turned down. A rejected proposal is
worth as much as an accepted one provided the reason is written down, so here they are. If you are
about to open an issue for one of these, read the reason first — and if the reason is wrong, say
so, because that is a more interesting conversation than the original proposal.

**Making `Next()` stop when a handler returns without calling it.** The proposal was to make `Next`
an `if` rather than a `for`, so that not calling `Next` short-circuits the chain the way
`func(http.Handler) http.Handler` middleware does. It trades a known footgun for a quieter one:
middleware that forgot to call `Next` would then silently return an empty `200`, and that failure is
invisible to tests that only assert status codes. The current loop is also what Gin does, so it is
the idiom most people arriving here already have in their fingers. The genuine risk — middleware
that writes a `401` and returns without `Abort()` — is a documentation problem, and it is documented.

**Restoring standard `WriteHeader` commit semantics.** `WriteHeader` records the status without
sending it; `WriteHeaderNow`, the first write, or a flush sends it. This looks like a deviation from
`net/http` because it is one, and it is load-bearing: it is what lets a later handler change a
status an earlier one set, and what lets a logger report a status it never set itself. Restoring
immediate commit would break both.

**Dropping `Flusher`/`Hijacker` from `ResponseWriter`.** Raised as making integration awkward,
especially under HTTP/2 where hijacking is unavailable. The premise does not hold: `Flush`
type-asserts the underlying writer and no-ops when it is not a `Flusher`, and `Hijack` returns a
plain error when the underlying writer is not a `Hijacker`. Both already degrade gracefully, and
keeping them in the interface is what stops a wrapper from silently dropping streaming or upgrade
support.

**Making `Copy()` return a distinct snapshot type.** Declined as an API change and adopted as a
documentation fix instead. The stated mechanism — that `*http.Request` is mutable shared state
reused across requests — is not how `net/http` works; request objects are not pooled. The real
hazard is narrower: the body is closed once the handler returns, so a goroutine holding a copy must
not read it. That is now said explicitly on `Copy`.

**A `Freeze()` call after route registration.** Route registration mutates the trees without
synchronisation, so it has to finish before serving starts. That is worth documenting — and it is
documented, on `Engine`, in the README, and in the contributing guide — but `Freeze()` would add
API surface and a hot-path check to prevent a mistake that shows up immediately in any test.

**Counting wildcard segments rather than `:` and `*` characters in `countParams`.** Raised on the
grounds that one of those bytes sitting in otherwise static route text would inflate `maxParams`
and over-reserve the per-`Context` parameter buffer. There is no such thing in this router:
`findWildcard` treats any `:` or `*` as opening a wildcard wherever it appears, so a route
containing one either declares a wildcard there or is rejected at registration. The count already
matches the grammar exactly.

**Hardening `SecureDir` against TOCTOU with `openat2`.** `RESOLVE_BENEATH` would close the
resolve-then-open window, but it is Linux-only with no Windows equivalent — so it would buy a
guarantee on one platform while the documentation still had to describe the other. The realistic
threat, a symlink already sitting in a checkout or an upload directory, is closed today. An attacker
who can create symlinks in your web root while requests are in flight has a larger problem than
static file serving.

**Functional options for the middleware constructors,** instead of the `XWithConfig(cfg)` structs
used now. `CORSConfig` alone would become seven exported `WithX` functions, roughly twenty-five
across `middleware/`, against a project that treats a small public surface as a goal. The usual
argument for options — that adding a field is otherwise breaking — does not apply, because a config
struct grows compatibly as long as callers use field names. And the current shape is the standard
library's own: `http.Server` and `http.Transport` are configured exactly this way. The zero values
are already the safe ones.

**Starting `nodeType` at 1 with an `Unknown` sentinel.** Zero meaning `static` is correct here
rather than accidental — a node with no explicit type *is* a static node, and several construction
sites rely on it. Reserving zero for an invalid value would mean setting `nType` explicitly
everywhere to avoid a state that cannot currently occur.

**Replacing `Params.ByName`'s linear scan.** Routes carry a handful of params, and a linear scan
over a small slice beats hashing on every realistic input. Raised, discussed, kept.

**A reported catch-all lookup panic.** A review reported that `getValue` descends to `children[0]`,
treats the empty intermediate as the catch-all leaf, and panics on `n.path[2:]`, making every
`/*name` route and all of `Static` unusable. It does not happen, and the misreading is one step of a
two-step descent: the static parent has `wildChild` false, so the walk reaches the intermediate
through the indices scan; the intermediate is the node with `wildChild` true, and it is *its*
`children[0]` the switch runs on. `TestCatchAllEndToEnd` and `TestCatchAllNodeShape` now pin both
the behaviour and the node shape, so the next reviewer can check the claim against a test rather
than against a reading.
