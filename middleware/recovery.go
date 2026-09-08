// Package middleware holds zarp's stock middleware.
//
// It is an optional import — core never depends on it, which is why zarp has
// no Default() constructor bundling a logger and recovery. Wire what you want
// yourself:
//
//	r := zarp.New()
//	r.Use(middleware.Default()...) // Logger + Recovery
package middleware

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/gozarp/zarp"
)

// Default returns the middleware most services want: a logger and recovery, in
// that order, so the logger records the 500 that recovery produces.
func Default() []zarp.HandlerFunc {
	return []zarp.HandlerFunc{Logger(), Recovery()}
}

// Recovery returns middleware that turns a panic in a later handler into a 500,
// logging the stack to stderr, so one bad request does not take the process
// down with it.
func Recovery() zarp.HandlerFunc {
	return RecoveryWithWriter(os.Stderr)
}

// RecoveryWithWriter is Recovery logging to out.
func RecoveryWithWriter(out io.Writer) zarp.HandlerFunc {
	return RecoveryWithHandler(func(c *zarp.Context, err any) {
		if out != nil {
			fmt.Fprintf(out, "[zarp] panic recovered: %s %s\n%v\n%s\n",
				c.Request.Method, c.Request.URL.Path, err, debug.Stack())
		}
		c.AbortWithStatus(http.StatusInternalServerError)
	})
}

// RecoveryWithHandler is Recovery with your own response to a panic. handle is
// called with the recovered value and should end the request — the chain is not
// resumed either way.
func RecoveryWithHandler(handle func(c *zarp.Context, err any)) zarp.HandlerFunc {
	return func(c *zarp.Context) {
		// One deferred call per request is the price of not crashing.
		defer func() {
			err := recover()
			if err == nil {
				return
			}

			// net/http panics with ErrAbortHandler to abandon a response on
			// purpose. Swallowing it would break the server's own handling, so
			// it goes back up untouched.
			if e, ok := err.(error); ok && errors.Is(e, http.ErrAbortHandler) {
				panic(err)
			}

			// The client hung up mid-write. Nothing can be sent, and it is not
			// a crash worth a stack trace — abandon the request quietly.
			if isBrokenPipe(err) {
				c.Abort()
				return
			}

			handle(c, err)
		}()

		c.Next()
	}
}

// isBrokenPipe reports whether err is the connection going away underneath a
// write, rather than a bug.
//
// The syscall checks cover Unix; Windows reports the same conditions through
// WSA error numbers that do not match those constants, so the message is
// checked too. Both are needed for this to work everywhere.
func isBrokenPipe(recovered any) bool {
	err, ok := recovered.(error)
	if !ok {
		return false
	}

	var opErr *net.OpError
	if !errors.As(err, &opErr) {
		return false
	}

	if errors.Is(opErr, syscall.EPIPE) || errors.Is(opErr, syscall.ECONNRESET) {
		return true
	}

	var sysErr *os.SyscallError
	if !errors.As(opErr, &sysErr) {
		return false
	}
	msg := strings.ToLower(sysErr.Error())
	return strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset by peer") ||
		strings.Contains(msg, "an established connection was aborted") ||
		strings.Contains(msg, "forcibly closed by the remote host")
}
