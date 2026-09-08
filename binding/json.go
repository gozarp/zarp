package binding

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/subhanjanops/gomicro"
)

// MaxBodyBytes caps how much of a request body the JSON binder will read. An
// uncapped decode over an attacker-controlled body is a memory-exhaustion
// vector, so this is on by default; set it to 0 to lift the cap.
var MaxBodyBytes int64 = 4 << 20 // 4 MB

// EnableDecoderUseNumber makes the decoder read numbers into any as
// json.Number rather than float64, which preserves large integers exactly.
var EnableDecoderUseNumber bool

// EnableDecoderDisallowUnknownFields makes a body carrying a field the target
// struct does not have an error rather than something silently dropped.
var EnableDecoderDisallowUnknownFields bool

// ErrEmptyBody reports a request that carried no body at all. It is separated
// from other decode failures because "EOF" is a useless thing to show an API
// consumer.
var ErrEmptyBody = errors.New("binding: request body is empty")

type jsonBinding struct{}

func (jsonBinding) Name() string { return "json" }

func (jsonBinding) Bind(c *gomicro.Context, obj any) error {
	if c.Request == nil || c.Request.Body == nil {
		return ErrEmptyBody
	}

	body := io.Reader(c.Request.Body)
	if MaxBodyBytes > 0 {
		// MaxBytesReader lets net/http close the connection rather than read a
		// body it is going to reject.
		body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxBodyBytes)
	}

	decoder := json.NewDecoder(body)
	if EnableDecoderUseNumber {
		decoder.UseNumber()
	}
	if EnableDecoderDisallowUnknownFields {
		decoder.DisallowUnknownFields()
	}

	if err := decoder.Decode(obj); err != nil {
		if errors.Is(err, io.EOF) {
			return ErrEmptyBody
		}
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return fmt.Errorf("binding: request body exceeds %d bytes", maxErr.Limit)
		}
		return fmt.Errorf("binding: %w", err)
	}
	return nil
}
