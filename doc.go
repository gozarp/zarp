// Package zarp is a lightweight HTTP framework: expressive routing and
// middleware with stdlib-level overhead, built on the standard library alone.
//
// # Getting started
//
// An Engine is an http.Handler, which is the real integration point:
//
//	r := zarp.New()
//
//	r.GET("/ping", func(c *zarp.Context) {
//		c.JSON(http.StatusOK, map[string]string{"message": "pong"})
//	})
//
//	api := r.Group("/api/v1", authMiddleware)
//	api.GET("/users/:id", func(c *zarp.Context) {
//		c.JSON(http.StatusOK, User{ID: c.Param("id")})
//	})
//
//	r.Run(":8080") // or build your own http.Server around r
//
// # Routing
//
// Paths are matched by a radix tree, one per HTTP method, in time proportional
// to the path length. A segment may be a literal, a named parameter (":id"), or
// a catch-all ("*filepath") that must end the route. Conflicting registrations
// panic at startup rather than resolving ambiguously at request time:
// "/user/:id" and "/user/new" cannot both exist.
//
// Matched values are read with Context.Param, and the route pattern that
// matched is Context.FullPath — log that rather than the URL, since it has
// bounded cardinality.
//
// # Middleware
//
// Middleware and handlers are the same type. What makes a handler middleware is
// that it calls Context.Next, running the rest of the chain and resuming
// afterwards:
//
//	func Timer(c *zarp.Context) {
//		start := time.Now()
//		c.Next()
//		log.Printf("%s took %s", c.FullPath(), time.Since(start))
//	}
//
// Context.Abort stops the chain at every level of the stack; the handler itself
// still has to return.
//
// A chain is resolved and copied when the route is registered, so Use affects
// only routes registered after it.
//
// # Performance
//
// The design has three load-bearing pieces: routes live in a radix tree rather
// than a map or a regex list, each request borrows its Context from a
// sync.Pool, and route parameters are a flat slice rather than a map. Together
// they make a request allocation-free from ServeHTTP through the handler chain.
//
// The costs that remain are the caller's: encoding a JSON body, or passing a
// value to a formatting verb. Context.Text exists so that writing a variable
// string does not reach fmt at all.
//
// Two rules follow from pooling, and both matter:
//
//   - A Context must not outlive its handler. Once the handler returns, the
//     Context goes back to the pool and another request overwrites it. Use
//     Context.Copy for anything that runs later, such as a goroutine.
//   - Every field added to Context must be cleared in its reset, or one
//     request's data leaks into another's.
//
// # Optional packages
//
// The core package imports nothing outside the standard library, and nothing
// from the framework's own subpackages. Request binding, validation, alternate
// renderers and the stock middleware live in their own packages, so a program
// that does not use them does not pay for them.
package zarp
