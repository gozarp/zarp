// Copyright 2026 Subhanjan Adhikary. All rights reserved.
// Use of this source code is governed by a MIT style
// license that can be found in the LICENSE file.

// Command middleware shows the chain: stock middleware, groups, a custom auth
// guard that aborts, and a logger sink pointed at log/slog.
//
//	go run ./examples/middleware
//	curl localhost:8080/public
//	curl localhost:8080/admin/secret                    # 401
//	curl -H "Authorization: Bearer letmein" localhost:8080/admin/secret
//	curl localhost:8080/boom                            # 500, process survives
package main

import (
	"log"
	"log/slog"
	"net/http"
	"os"

	"github.com/gozarp/zarp"
	"github.com/gozarp/zarp/middleware"
)

func main() {
	r := zarp.New()

	// The logger writes plain text to stderr by default. Any logger plugs in
	// through Sink — here, the standard library's.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	r.Use(
		middleware.LoggerWith(middleware.SlogSink(logger)),
		middleware.Recovery(),
		middleware.RequestID(),
	)

	r.GET("/public", func(c *zarp.Context) {
		c.JSON(http.StatusOK, map[string]string{
			"message":   "no auth needed",
			"requestID": middleware.GetRequestID(c),
		})
	})

	// A group's middleware runs only for routes registered through it.
	admin := r.Group("/admin", requireToken("letmein"))
	admin.GET("/secret", func(c *zarp.Context) {
		c.JSON(http.StatusOK, map[string]string{"message": "the secret"})
	})

	// Recovery turns this into a 500 and keeps the server up.
	r.GET("/boom", func(c *zarp.Context) {
		panic("something went wrong")
	})

	log.Println("listening on :8080")
	if err := r.Run(":8080"); err != nil {
		log.Fatal(err)
	}
}

// requireToken is an ordinary handler. What makes it middleware is Next; what
// makes it a guard is Abort, which stops every later handler in the chain.
func requireToken(token string) zarp.HandlerFunc {
	want := "Bearer " + token

	return func(c *zarp.Context) {
		if c.GetHeader("Authorization") != want {
			c.AbortWithStatusJSON(http.StatusUnauthorized,
				map[string]string{"error": "unauthorized"})
			return
		}
		c.Next()
	}
}
