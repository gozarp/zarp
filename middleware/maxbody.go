// Copyright 2026 Subhanjan Adhikary. All rights reserved.
// Use of this source code is governed by a MIT style
// license that can be found in the LICENSE file.

package middleware

import (
	"net/http"
	"strconv"

	"github.com/gozarp/zarp"
)

// MaxBodySize caps how many bytes any handler below it can read from the
// request body.
//
//	r.Use(middleware.MaxBodySize(8 << 20)) // 8 MB
//
// A body limit is a cross-cutting control: it should not depend on whether a
// handler happens to bind JSON, parse a form, or read c.Request.Body itself.
// This wraps the body once, before routing, so every one of those paths is
// bounded by the same number.
//
// Reading past the limit fails the read rather than the request — a Read
// returns *http.MaxBytesError, which binding reports as an error and a handler
// reading the body itself should answer with 413. The limit is applied to the
// writer net/http handed us rather than to the Context's wrapper, because
// MaxBytesReader tells the server an oversized body arrived through a method
// only net/http's own response has; without that the connection would be kept
// open and drained instead.
//
// The engine's MaxMultipartMemory is a different setting: it bounds how much of
// a multipart body is buffered in memory before spilling to disk, not how large
// the body may be.
// A limit of zero or less panics rather than being honoured. net/http would
// read it as "no bytes at all", so an unset configuration value would silently
// reject every request that carries a body — a failure mode that looks like a
// client problem and is not.
func MaxBodySize(n int64) zarp.HandlerFunc {
	if n <= 0 {
		panic("zarp: MaxBodySize needs a positive limit, got " + strconv.FormatInt(n, 10))
	}
	return func(c *zarp.Context) {
		if c.Request != nil && c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer.Unwrap(), c.Request.Body, n)
		}
		c.Next()
	}
}
