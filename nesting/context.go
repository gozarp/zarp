package nesting

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
)

const (
	// noWritten marks a response that has not written a byte yet, which is
	// distinct from one that wrote zero bytes.
	noWritten = -1

	defaultStatus = http.StatusOK
)

type ResponseWriter interface {
	http.ResponseWriter
	http.Flusher
	http.Hijacker
	Status() int
	Size() int
	Written() bool
	WriteHeaderNow()
}

// responseWriter wraps the http.ResponseWriter handed to ServeHTTP and records
// what was done to it, so middleware can log the status and size after the
// handler has returned.
//
// It is stored in Context by value: a pointer field would cost a second
// allocation per request and defeat the pool.
type responseWriter struct {
	http.ResponseWriter
	size   int
	status int
}

func (w *responseWriter) reset(rw http.ResponseWriter) {
	w.ResponseWriter = rw
	w.size = noWritten
	w.status = defaultStatus
}

// WriteHeader records the status without sending it, so a later handler in the
// chain can still change it. WriteHeaderNow sends it.
func (w *responseWriter) WriteHeader(code int) {
	if code > 0 && w.status != code {
		if w.Written() {
			// The header is already on the wire; changing it now would only
			// earn a "superfluous WriteHeader" from net/http.
			return
		}
		w.status = code
	}
}

func (w *responseWriter) WriteHeaderNow() {
	if !w.Written() {
		w.size = 0
		w.ResponseWriter.WriteHeader(w.status)
	}
}

func (w *responseWriter) Write(data []byte) (n int, err error) {
	w.WriteHeaderNow()
	n, err = w.ResponseWriter.Write(data)
	w.size += n
	return
}

// WriteString avoids the []byte conversion a plain Write would need.
func (w *responseWriter) WriteString(s string) (n int, err error) {
	w.WriteHeaderNow()
	n, err = io.WriteString(w.ResponseWriter, s)
	w.size += n
	return
}

func (w *responseWriter) Status() int   { return w.status }
func (w *responseWriter) Size() int     { return w.size }
func (w *responseWriter) Written() bool { return w.size != noWritten }

// Flush forwards to the underlying writer, sending the status first so a
// streaming response does not stall waiting for a header that never went out.
func (w *responseWriter) Flush() {
	w.WriteHeaderNow()
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if !w.Written() {
		w.size = 0
	}
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("gomicro: the ResponseWriter does not implement http.Hijacker")
	}
	return h.Hijack()
}

// Context carries one request through the handler chain. It is pooled: every
// field must be cleared by reset, and it must not escape the handler — see Copy.
type Context struct {
	Writer     ResponseWriter
	Request    *http.Request
	Params     Params
	handlers   []HandlerFunc
	queryCache url.Values
	keys       map[string]any
	engine     any // becomes *Engine in build order step 3
	fullPath   string
	writermem  responseWriter
	index      int8
}

// reset clears every field for the next request. Field order matches the struct
// declaration so a missing line is visible by eye; a field left behind here is a
// cross-request data leak.
func (c *Context) reset(w http.ResponseWriter, r *http.Request) {
	c.writermem.reset(w)
	c.Writer = &c.writermem
	c.Request = r
	c.Params = c.Params[:0] // keep the backing array sized by Router.maxParams
	c.handlers = nil
	c.queryCache = nil
	c.keys = nil
	c.fullPath = ""
	c.index = -1
}
