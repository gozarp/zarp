package zarp

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func newContext(w http.ResponseWriter, r *http.Request) *Context {
	c := &Context{}
	c.reset(w, r)
	return c
}

func TestResponseWriterImplementsInterface(t *testing.T) {
	var _ ResponseWriter = (*responseWriter)(nil)
}

func TestResponseWriterDefaults(t *testing.T) {
	rec := httptest.NewRecorder()
	c := newContext(rec, httptest.NewRequest("GET", "/", nil))

	if c.Writer.Status() != http.StatusOK {
		t.Errorf("status = %d, want 200", c.Writer.Status())
	}
	if c.Writer.Written() {
		t.Error("Written() true before any write")
	}
	if c.Writer.Size() != noWritten {
		t.Errorf("size = %d, want %d", c.Writer.Size(), noWritten)
	}
}

func TestResponseWriterDeferredStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	c := newContext(rec, httptest.NewRequest("GET", "/", nil))

	c.Writer.WriteHeader(http.StatusTeapot)
	if rec.Code != http.StatusOK {
		t.Errorf("recorder saw %d already; WriteHeader must defer", rec.Code)
	}
	// A later handler in the chain may still change it.
	c.Writer.WriteHeader(http.StatusCreated)
	c.Writer.WriteHeaderNow()
	if rec.Code != http.StatusCreated {
		t.Errorf("recorder code = %d, want 201", rec.Code)
	}
	if !c.Writer.Written() {
		t.Error("Written() false after WriteHeaderNow")
	}
}

func TestResponseWriterStatusLockedAfterWrite(t *testing.T) {
	rec := httptest.NewRecorder()
	c := newContext(rec, httptest.NewRequest("GET", "/", nil))

	c.Writer.WriteHeader(http.StatusCreated)
	if _, err := c.Writer.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	c.Writer.WriteHeader(http.StatusTeapot) // must be ignored

	if c.Writer.Status() != http.StatusCreated {
		t.Errorf("status = %d, want 201 (locked after write)", c.Writer.Status())
	}
	if c.Writer.Size() != 5 {
		t.Errorf("size = %d, want 5", c.Writer.Size())
	}
	if rec.Body.String() != "hello" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestResponseWriterImplicitHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	c := newContext(rec, httptest.NewRequest("GET", "/", nil))

	if _, err := c.writermem.WriteString("hi"); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || c.Writer.Size() != 2 {
		t.Errorf("code=%d size=%d, want 200/2", rec.Code, c.Writer.Size())
	}
}

func TestResponseWriterAccumulatesSize(t *testing.T) {
	rec := httptest.NewRecorder()
	c := newContext(rec, httptest.NewRequest("GET", "/", nil))

	c.Writer.Write([]byte("abc"))
	c.writermem.WriteString("de")
	if c.Writer.Size() != 5 {
		t.Errorf("size = %d, want 5", c.Writer.Size())
	}
}

// flushRecorder reports whether Flush reached the underlying writer.
type flushRecorder struct {
	http.ResponseWriter
	flushed bool
}

func (f *flushRecorder) Flush() { f.flushed = true }

func TestResponseWriterFlushForwards(t *testing.T) {
	fr := &flushRecorder{ResponseWriter: httptest.NewRecorder()}
	c := newContext(fr, httptest.NewRequest("GET", "/", nil))

	c.Writer.Flush()
	if !fr.flushed {
		t.Error("Flush did not reach the underlying writer")
	}
	if !c.Writer.Written() {
		t.Error("Flush must send the status first")
	}
}

func TestResponseWriterHijackUnsupported(t *testing.T) {
	c := newContext(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if _, _, err := c.Writer.Hijack(); err == nil {
		t.Error("Hijack on a non-Hijacker: want an error, got nil")
	}
}

type hijackRecorder struct {
	http.ResponseWriter
	called bool
}

func (h *hijackRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.called = true
	return nil, nil, nil
}

func TestResponseWriterHijackForwards(t *testing.T) {
	hr := &hijackRecorder{ResponseWriter: httptest.NewRecorder()}
	c := newContext(hr, httptest.NewRequest("GET", "/", nil))

	if _, _, err := c.Writer.Hijack(); err != nil {
		t.Fatal(err)
	}
	if !hr.called {
		t.Error("Hijack did not reach the underlying writer")
	}
	if c.Writer.Size() != 0 {
		t.Errorf("size = %d, want 0 after hijack", c.Writer.Size())
	}
}

// TestResetClearsEveryField is the highest-value test in this file: it fails
// automatically when a field is added to Context without a matching line in
// reset, which is the cross-request data-leak bug class.
func TestResetClearsEveryField(t *testing.T) {
	c := &Context{
		Writer:     nil,
		Request:    httptest.NewRequest("GET", "/old", nil),
		Params:     Params{{"leak", "value"}},
		handlers:   []HandlerFunc{func(*Context) {}},
		queryCache: map[string][]string{"a": {"b"}},
		keys:       map[string]any{"secret": 1},
		engine:     New(),
		fullPath:   "/old/:id",
		index:      7,
	}
	c.writermem.size = 99
	c.writermem.status = http.StatusTeapot

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/new", nil)
	c.reset(rec, req)

	// Fields reset deliberately to a live value rather than the zero value.
	if c.Writer != ResponseWriter(&c.writermem) {
		t.Error("Writer must point at the embedded writermem")
	}
	if c.Request != req {
		t.Error("Request not re-pointed")
	}
	if c.index != -1 {
		t.Errorf("index = %d, want -1", c.index)
	}
	if c.writermem.status != defaultStatus || c.writermem.size != noWritten {
		t.Errorf("writermem = %+v, want status %d size %d", c.writermem, defaultStatus, noWritten)
	}

	// Everything else must be zero. engine is deliberately not in that set: it
	// belongs to the pooled Context for its whole life, not to one request.
	live := map[string]bool{
		"Writer": true, "Request": true, "index": true, "writermem": true, "engine": true,
	}
	v := reflect.ValueOf(*c)
	typ := v.Type()
	for i := range typ.NumField() {
		name := typ.Field(i).Name
		if live[name] {
			continue
		}
		f := v.Field(i)
		switch f.Kind() {
		case reflect.Slice, reflect.Map, reflect.String:
			if f.Len() != 0 {
				t.Errorf("field %s not cleared: %v", name, f)
			}
		case reflect.Interface, reflect.Pointer:
			if !f.IsNil() {
				t.Errorf("field %s not cleared: %v", name, f)
			}
		default:
			if !f.IsZero() {
				t.Errorf("field %s not cleared: %v", name, f)
			}
		}
	}
}

func TestResetKeepsParamsCapacity(t *testing.T) {
	c := &Context{Params: make(Params, 0, 8)}
	c.Params = append(c.Params, Param{"a", "b"})
	before := cap(c.Params)

	c.reset(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	if len(c.Params) != 0 {
		t.Errorf("len = %d, want 0", len(c.Params))
	}
	if cap(c.Params) != before {
		t.Errorf("cap = %d, want %d — reset must not re-make the buffer", cap(c.Params), before)
	}
}

func BenchmarkContextReset(b *testing.B) {
	c := &Context{Params: make(Params, 0, 4)}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/user/42", nil)
	b.ReportAllocs()
	for b.Loop() {
		c.reset(rec, req)
	}
}
