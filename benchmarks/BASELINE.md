# Benchmark baseline

Checked in so a regression is visible without re-running the old code. Compare against it with
`benchstat`, and update it deliberately — in the same PR as whatever moved the numbers, with the
reason in the commit message.

```
go1.25.0 windows/amd64 · AMD Ryzen 7 7435HS · 16 threads · -count=5, averaged
```

Reproduce:

```sh
go test -bench=. -benchmem -run XXX -count=5 ./benchmarks/           # end to end
go test -bench='Router|ServeMux|Context' -benchmem -run XXX -count=5 .
go test -bench=. -benchmem -run XXX -count=5 ./middleware/ ./binding/ ./render/
```

**The number that matters is allocs/op.** Ns/op moves with the machine; an allocation appearing
where there was none is a design regression, and every 0 below is load-bearing.

---

## End to end, against `net/http`

A whole request through `ServeHTTP`: pool, route, chain, handler, response. Measured on a writer
that implements `io.StringWriter`, as `net/http`'s own response does — without that, `io.WriteString`
falls back to a `[]byte` conversion and the benchmark charges an allocation production never pays.

| | gomicro | `net/http.ServeMux` |
|---|---|---|
| static route | **52.4 ns**, 0 allocs | 94.4 ns, 1 alloc |
| param route | **59.9 ns**, 0 allocs | 186.7 ns, 2 allocs |
| 404 | **56.4 ns**, 0 allocs | — |
| 405 + `Allow` | 136.9 ns, 2 allocs | — |
| trailing-slash redirect | **30.3 ns**, 0 allocs | — |
| grouped route, 2 middlewares | **62.6 ns**, 0 allocs | — |
| parallel, 16 cores | **8.0 ns**, 0 allocs | — |

405 is the one path that allocates: it walks every other method tree and builds the `Allow`
value. That is why `HandleMethodNotAllowed` is off by default.

`BenchmarkEngineStringFmt` (119.1 ns, 1 alloc) is the same route as `EngineParam` written with
`c.String(code, "%s", v)` instead of `c.Text(code, v)`. The gap is `fmt` boxing the argument —
the handler's allocation, not the framework's.

## Middleware

| | ns/op | allocs |
|---|---|---|
| no middleware (baseline) | 51.9 | 0 |
| chain of 1 | 52.0 | 0 |
| chain of 3 | 57.7 | 0 |
| chain of 10 | 78.4 | 0 |
| `Recovery` | 59.5 | 0 |
| `Logger` (TextSink → `io.Discard`) | 346.4 | 0 |
| `CORS` | 229.0 | 1 |
| `RequestID` | 516.3 | 5 |

**~2.8 ns per middleware hop**, allocation-free — the index cursor, rather than a closure and a
stack frame per layer.

## Router only

Route resolution alone, over a 12-route API-shaped set. `ServeMux.Handler()` does more than match
a tree — path cleaning, host matching, storing wildcards in the request context, which is where
its allocations come from — so read this as *resolution is allocation-free and about an order of
magnitude cheaper*, not as a claim about whole requests. The table above is the honest one.

| | gomicro | `ServeMux` |
|---|---|---|
| static | **15.8 ns**, 0 allocs | 139.3 ns, 0 allocs |
| 1 param | **18.7 ns**, 0 allocs | 163.3 ns, 1 alloc |
| 3 params | **38.7 ns**, 0 allocs | 364.8 ns, 3 allocs |
| catch-all | **36.3 ns**, 0 allocs | 884.2 ns, 9 allocs |
| miss | **11.4 ns**, 0 allocs | 575.5 ns, 9 allocs |

## Context

| | ns/op | allocs |
|---|---|---|
| `reset` | 3.9 | 0 |
| `Param` | 4.7 | 0 |
| `GetString` | 9.3 | 0 |
| `Query` (cached) | 13.1 | 0 |
| `Text` | 28.2 | 0 |
| `String` (no args) | 28.9 | 0 |
| `String` (`"%s"`, v) | 58.1 | 0 |
| `JSON` (small struct) | 292.2 | 1 |

The one `JSON` allocation is the caller boxing a struct into `any`, not gomicro.

## Optional packages

These are opt-in, and they cost what they cost — reflection and encoding are not free, which is
exactly why they are not in the core.

| | ns/op | allocs |
|---|---|---|
| `render.JSON` | 318.6 | 4 |
| `Context.JSON` (same payload) | 229.2 | 1 |
| `binding.Query` (5 fields + validation) | 1399.4 | 7 |
| `binding.Validate` (6 rules) | 432.8 | 4 |
| `middleware.SinkText` | 344.4 | 0 |
| `middleware.SinkSlog` | 1249.2 | 1 |

`render.JSON` against `Context.JSON` is the price of the extensible path: an interface call plus
a renderer value. That gap is why core keeps its own direct writer and does not route through
`Render` — and a test asserts the two produce identical bytes, so the duplication cannot drift.
