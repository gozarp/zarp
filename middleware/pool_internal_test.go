package middleware

import (
	"io"
	"strings"
	"testing"
	"time"
)

// TestWriteTextDoesNotPoolAnEnormousLine covers what maxPooledLine is for.
//
// Entry.Path is the route pattern for a matched route, but the requested URL
// for a miss — and net/http bounds that only by MaxHeaderBytes, a megabyte by
// default. Without the cap, one such request leaves a megabyte-wide buffer in
// the pool for the life of the process, per P.
//
// The observable property is what the pool hands out next, so that is what this
// asserts rather than the arithmetic inside releaseLine.
func TestWriteTextDoesNotPoolAnEnormousLine(t *testing.T) {
	huge := "/" + strings.Repeat("a", 64<<10)

	var sb strings.Builder
	writeText(&sb, Entry{
		Start:    time.Now(),
		Method:   "GET",
		Path:     huge,
		Status:   404,
		ClientIP: "192.0.2.1",
	})

	if !strings.Contains(sb.String(), huge) {
		t.Fatal("the path was not written in full; the cap must not truncate output")
	}

	ptr := lineBuffers.Get().(*[]byte)
	defer lineBuffers.Put(ptr)
	if cap(*ptr) > maxPooledLine {
		t.Errorf("pool returned a buffer of cap %d, over the %d limit: the huge line was retained",
			cap(*ptr), maxPooledLine)
	}

	// The sink still works once the oversized buffer has been dropped.
	writeText(io.Discard, Entry{Start: time.Now(), Method: "GET", Path: "/ok", Status: 200})
}

// TestWriteTextStillPoolsOrdinaryLines is the other half: the cap must not turn
// the pool off for the traffic it exists to serve.
func TestWriteTextStillPoolsOrdinaryLines(t *testing.T) {
	// Drain whatever this P is holding so the buffer under test is ours.
	for range 4 {
		lineBuffers.Get()
	}

	writeText(io.Discard, Entry{
		Start:    time.Now(),
		Method:   "GET",
		Path:     "/user/:id",
		Status:   200,
		ClientIP: "192.0.2.1",
	})

	ptr := lineBuffers.Get().(*[]byte)
	defer lineBuffers.Put(ptr)

	// A pooled buffer carries the bytes of the line just written; a freshly
	// allocated one from New would be empty.
	if len(*ptr) == 0 {
		t.Error("an ordinary line was not returned to the pool")
	}
	if cap(*ptr) > maxPooledLine {
		t.Errorf("ordinary line grew to cap %d", cap(*ptr))
	}
}
