package binding

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/gozarp/zarp"
)

// DefaultMaxBodyBytes is the body cap a JSONConfig gets when it does not set
// one. An uncapped decode over an attacker-controlled body is a
// memory-exhaustion vector, so the safe value is the one you get by default.
const DefaultMaxBodyBytes int64 = 4 << 20 // 4 MB

// ErrEmptyBody reports a request that carried no body at all. It is separated
// from other decode failures because "EOF" is a useless thing to show an API
// consumer.
var ErrEmptyBody = errors.New("binding: request body is empty")

// ErrTrailingContent reports a body that carried more than one JSON value.
var ErrTrailingContent = errors.New("binding: request body must contain exactly one JSON value")

// JSONConfig configures a JSON binder.
//
// The zero value is the safe one: a 4 MB cap, numbers decoded normally, and
// unknown fields ignored. Configuration is per binder rather than per process,
// so one route can accept a 50 MB upload without every other route doing so
// too, and so two tests can run in parallel without fighting over a global.
type JSONConfig struct {
	// MaxBodyBytes caps how much of the body is read. Zero means
	// DefaultMaxBodyBytes; a negative value removes the cap, which is only
	// sensible when something upstream imposes one.
	MaxBodyBytes int64

	// UseNumber decodes numbers into any as json.Number rather than float64,
	// which preserves large integers exactly.
	UseNumber bool

	// DisallowUnknownFields makes a body carrying a field the target struct
	// does not have an error rather than something silently dropped.
	DisallowUnknownFields bool
}

// JSONWith returns a JSON binder using cfg.
//
//	var uploads = binding.JSONWith(binding.JSONConfig{MaxBodyBytes: 50 << 20})
//
//	func handler(c *zarp.Context) {
//		var req Upload
//		if err := binding.With(c, &req, uploads); err != nil { ... }
//	}
func JSONWith(cfg JSONConfig) Binding {
	if cfg.MaxBodyBytes == 0 {
		cfg.MaxBodyBytes = DefaultMaxBodyBytes
	}
	return jsonBinding{cfg: cfg}
}

type jsonBinding struct{ cfg JSONConfig }

func (jsonBinding) Name() string { return "json" }

func (b jsonBinding) Bind(c *zarp.Context, obj any) error {
	if c.Request == nil || c.Request.Body == nil {
		return ErrEmptyBody
	}

	body := io.Reader(c.Request.Body)
	if b.cfg.MaxBodyBytes > 0 {
		// Unwrap, not c.Writer: MaxBytesReader signals "this request was too
		// large" through a method only net/http's own response writer has, so
		// handing it the wrapper would silently lose the signal and leave the
		// server draining a body it has already rejected.
		body = http.MaxBytesReader(c.Writer.Unwrap(), c.Request.Body, b.cfg.MaxBodyBytes)
	}

	decoder := json.NewDecoder(body)
	if b.cfg.UseNumber {
		decoder.UseNumber()
	}
	if b.cfg.DisallowUnknownFields {
		decoder.DisallowUnknownFields()
	}

	if err := decoder.Decode(obj); err != nil {
		return decodeError(err)
	}

	// One request carries one document. A body that decodes and then continues
	// — `{"a":1} {"b":2}`, or JSON followed by anything else — is a request two
	// parsers can disagree about, which is where smuggling bugs live. More
	// reports whether a value follows, skipping whitespace, so a trailing
	// newline is still a well-formed body.
	if decoder.More() {
		return ErrTrailingContent
	}
	return nil
}

// decodeError translates the decoder's failures into ones an API can act on.
func decodeError(err error) error {
	if errors.Is(err, io.EOF) {
		return ErrEmptyBody
	}
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return fmt.Errorf("binding: request body exceeds %d bytes", maxErr.Limit)
	}
	return fmt.Errorf("binding: %w", err)
}
