package middleware

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/subhanjanops/gomicro"
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
	Generator func() string

	// TrustInbound reuses a client-supplied id, so a trace spans services.
	// Defaults to true; turn it off at the edge, where the value is attacker
	// controlled and only useful for confusing your own logs.
	TrustInbound *bool
}

// RequestID returns middleware that gives every request an id, stores it on the
// Context and echoes it in the response.
func RequestID() gomicro.HandlerFunc {
	return RequestIDWithConfig(RequestIDConfig{})
}

// RequestIDWithConfig returns a configured RequestID.
func RequestIDWithConfig(cfg RequestIDConfig) gomicro.HandlerFunc {
	header := cfg.Header
	if header == "" {
		header = RequestIDHeader
	}
	generate := cfg.Generator
	if generate == nil {
		generate = newRequestID
	}
	trustInbound := true
	if cfg.TrustInbound != nil {
		trustInbound = *cfg.TrustInbound
	}

	return func(c *gomicro.Context) {
		id := ""
		if trustInbound {
			id = c.GetHeader(header)
			if !validRequestID(id) {
				id = ""
			}
		}
		if id == "" {
			id = generate()
		}

		c.Set(RequestIDKey, id)
		c.Header(header, id)
		c.Next()
	}
}

// GetRequestID returns the id RequestID stored, or "".
func GetRequestID(c *gomicro.Context) string {
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

// validRequestID rejects an inbound value that is empty, over-long, or carries
// anything but printable ASCII. It is echoed into a response header, so control
// characters in it are a header-injection bug waiting to happen.
func validRequestID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for i := range len(id) {
		if c := id[i]; c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}
