# Contributing to zarp

Thanks for taking an interest. zarp is a small framework with a narrow promise — Gin-like
ergonomics with no third-party dependencies and no allocations on the request path — and most of
the guidance below exists to keep that promise checkable rather than aspirational.

## Before you start

**Open an issue first for anything that changes the public API, adds a package, or touches the
router.** Small fixes, tests and documentation can go straight to a pull request.

Two categories of change are declined by default, so please raise them as an issue before writing
code:

- **A third-party dependency**, in any package. The module has zero, CI asserts it, and that is a
  feature rather than an accident.
- **A new feature in the core package** that belongs in an optional one. Rendering engines,
  validators, session stores and the like go in `render/`, `binding/` or a package of their own —
  never in the import graph of `zarp` itself.

## Getting set up

```sh
git clone https://github.com/gozarp/zarp
cd zarp
go build ./...
go test ./...
```

Go 1.25 or later. No other tooling is needed to build or test; `golangci-lint` and `benchstat` are
needed to reproduce the CI checks locally:

```sh
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.3
go install golang.org/x/perf/cmd/benchstat@latest
```

## The checks

```sh
go build ./...
go vet ./...
gofmt -l .                                         # must print nothing
golangci-lint run ./...                            # config in .golangci.yml

go test ./...
go test -race ./...
go test -cover . ./binding ./render ./middleware   # must stay at or above 90%
```

On Windows, ThreadSanitizer often fails to start (`ThreadSanitizer failed to allocate ... error
code: 87`). Run the race detector under Linux; CI's race job is the authoritative one:

```sh
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W)":/src -w /src golang:1.25 go test -race ./...
```

## Design constraints

These are not preferences, and a pull request that breaks one will be asked to justify it in
writing before review continues:

- **Radix tree routing** — not regex, not a linear scan. Children are selected through the
  `indices` byte string, never a map.
- **`sync.Pool` for `Context`** — zero per-request allocation for the context object.
- **Route params as `[]Param{Key, Value}`** — never `map[string]string`.
- **Middleware as an index-cursor chain** — `[]HandlerFunc` walked by `c.Next()`, not nested
  `func(http.Handler) http.Handler` closures.
- **No reflection in the hot path** — struct-tag binding and validation are opt-in and live in
  `binding/`.
- **No rendering engine in core** — templates and XML live in `render/`.

If a change adds an allocation, a reflection call, or a map lookup to the hot request path, say so
in the description and explain why it earns its cost.

## Performance changes

Any change to the router, the context, or the middleware chain needs a `benchstat` comparison in
the pull request description:

```sh
git stash                                                              # measure main first
go test -bench=. -benchmem -run XXX -count=10 ./benchmarks/ > old.txt
git stash pop
go test -bench=. -benchmem -run XXX -count=10 ./benchmarks/ > new.txt
benchstat old.txt new.txt
```

**The number that matters is allocs/op.** ns/op moves with the machine and with whatever else is
running; an allocation appearing where there was none is a design regression regardless of how the
timings look. If your change moves the numbers meaningfully, update
[benchmarks/BASELINE.md](benchmarks/BASELINE.md) in the same pull request and say why in the
commit message.

## Tests

- Tests live next to the code they cover (`router_test.go` beside `router.go`), not in a separate
  directory.
- `benchmarks/` holds no `Test` functions, which is what keeps `go test ./...` fast. Benchmarks run
  deliberately, via `-bench`.
- Router changes need coverage of static routes, `:param` routes, conflicting registrations,
  `*catchall`, and trailing-slash behaviour.
- Coverage over `.`, `binding/`, `render/` and `middleware/` is gated at 90%. New code should
  arrive with its tests rather than a follow-up.
- **Adding a field to `Context` means clearing it in `reset`.** `TestResetClearsEveryField` walks
  the struct by reflection and will fail otherwise — a field left behind leaks one request's data
  into the next. Fix `reset`; don't amend the test.

## Style

- Match the surrounding code. `gofmt` settles formatting; `golangci-lint` settles the rest.
- Exported symbols carry doc comments — `revive`'s `exported` rule is on.
- Write a deliberately ignored error as `_, _ = w.Write(b)` rather than leaving it bare, so the
  intent is visible and `errcheck` stays clean.
- Prefer explicit allocation-free code over clever abstractions in `context.go` and `router.go`
  specifically. Elsewhere, ordinary Go idioms are welcome.

## Commits and pull requests

Commit subjects follow [Conventional Commits](https://www.conventionalcommits.org): `feat:`,
`fix:`, `docs:`, `test:`, `refactor:`, `perf:`, `chore:`. Keep the subject in the imperative mood
and under about 70 characters, and use the body to explain why rather than what.

Before opening a pull request:

- [ ] `go build ./... && go vet ./... && gofmt -l .` (the last prints nothing)
- [ ] `golangci-lint run ./...` is clean
- [ ] `go test ./...` and `go test -race ./...` pass
- [ ] coverage stayed at or above 90%
- [ ] `benchstat` comparison included for any change to the router, context or chain
- [ ] allocs/op did not increase for any benchmark
- [ ] no new map, reflection call, or `fmt` call on the hot path — or a written justification
- [ ] any new `Context` field is cleared in `reset`

## Reporting bugs

Include the Go version, the OS, a minimal route registration and request that reproduce the
problem, and what you expected instead. For a routing bug, the exact set of registered patterns
matters — a radix tree behaves differently depending on what shares a prefix with what.

## Security

Please do not open a public issue for a security problem. Report it privately through GitHub's
[security advisories](https://github.com/gozarp/zarp/security/advisories/new) instead.

## License

Contributions are licensed under the [MIT License](LICENSE) that covers the project.
