package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"

	"github.com/gozarp/zarp"
)

// RequestIDHeader is the header read and written by RequestID.
const RequestIDHeader = "X-Request-Id"

// RequestIDKey is the Context key the id is stored under.
const RequestIDKey = "requestID"

// RequestIDConfig configures RequestID.
type RequestIDConfig struct {
	// Header to read and write. Defaults to X-Request-Id.
	Header string

	// Generator produces an id when there is no usable inbound one. Defaults
	// to 16 random bytes, hex encoded.
	//
	// Its output is held to the same grammar as an inbound id, and a value
	// that fails it is replaced by the default generator: whatever else is
	// true, the id this middleware sets is safe to put in a header and a log
	// line.
	Generator func() string

	// TrustInbound reuses a client-supplied id, so a trace spans services.
	//
	// Off by default. An inbound id is chosen by whoever made the request: at
	// the edge it is an attacker-controlled string that will be written into
	// every log line for that request and correlated across your systems. Turn
	// it on behind a gateway that sets the header itself.
	TrustInbound bool
}

// RequestID returns middleware that gives every request an id, stores it on the
// Context and echoes it in the response.
func RequestID() zarp.HandlerFunc {
	return RequestIDWithConfig(RequestIDConfig{})
}

// RequestIDWithConfig returns a configured RequestID.
func RequestIDWithConfig(cfg RequestIDConfig) zarp.HandlerFunc {
	header := cfg.Header
	if header == "" {
		header = RequestIDHeader
	}
	if !validHeaderName(header) {
		// A configuration error, and one that would otherwise surface as a
		// header nothing can read rather than as a mistake.
		panic("zarp: RequestID header name is not a valid HTTP field name: " + strconv.Quote(header))
	}
	generate := cfg.Generator
	if generate == nil {
		generate = newRequestID
	}
	return func(c *zarp.Context) {
		id := ""
		if cfg.TrustInbound {
			id = c.GetHeader(header)
			if !validRequestID(id) {
				id = ""
			}
		}
		if id == "" {
			id = generate()
			if !validRequestID(id) {
				// A custom generator handed back something that does not belong
				// in a header. Fall back rather than emit it.
				id = newRequestID()
			}
		}

		c.Set(RequestIDKey, id)
		c.Header(header, id)
		c.Next()
	}
}

// GetRequestID returns the id RequestID stored, or "".
func GetRequestID(c *zarp.Context) string {
	return c.GetString(RequestIDKey)
}

// newRequestID is 16 random bytes, hex encoded: enough to not collide, and no
// dependency on a UUID package for a value nothing parses.
func newRequestID() string {
	var b [16]byte
	// crypto/rand.Read never fails on any supported platform; since Go 1.24 it
	// panics rather than returning an error worth checking.
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// validHeaderName reports whether name is an RFC 9110 field name — a token, so
// no spaces, colons or control characters. Checked when the middleware is
// built, because a bad name is a mistake in the program rather than in a
// request.
func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := range len(name) {
		if !isTokenByte(name[i]) {
			return false
		}
	}
	return true
}

// isTokenByte reports whether c may appear in an RFC 9110 token.
func isTokenByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return false
}

// validRequestID accepts [A-Za-z0-9._:-]{1,128} and nothing else.
//
// The value is echoed into a response header and written into logs, so the
// grammar is deliberately narrower than "printable ASCII": rejecting control
// characters stops header injection, and rejecting quotes, spaces and brackets
// stops a caller forging fields in a structured log line. Every id format worth
// correlating — UUID, W3C trace id, ULID, this package's own hex — fits.
func validRequestID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for i := range len(id) {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.', c == '_', c == ':', c == '-':
		default:
			return false
		}
	}
	return true
}
