package middleware

import (
	"strings"
	"testing"
	"time"
)

// TestReleaseLineKeepsSmallBuffersAndDropsBigOnes covers what maxPooledLine is
// for.
//
// Entry.Path is the route pattern for a matched route, but the requested URL
// for a miss — and net/http bounds that only by MaxHeaderBytes, a megabyte by
// default. Without the cap, one such request leaves a megabyte-wide buffer in
// the pool for the life of the process, per P.
//
// The assertion is on releaseLine's own effect rather than on what the pool
// hands back afterwards: sync.Pool may drop anything at any time — a GC between
// the Put and the Get empties it — so a test that expects a buffer to come back
// out is testing something the standard library does not promise.
func TestReleaseLineKeepsSmallBuffersAndDropsBigOnes(t *testing.T) {
	tests := []struct {
		name     string
		capacity int
		wantKept bool
	}{
		{"an ordinary line", 160, true},
		{"exactly at the limit", maxPooledLine, true},
		{"one byte over", maxPooledLine + 1, false},
		{"a megabyte path", 1 << 20, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// A sentinel distinct from the buffer under test, so "was it kept"
			// is visible: releaseLine assigns through the pointer only when it
			// keeps the buffer.
			sentinel := make([]byte, 0, 1)
			ptr := &sentinel

			b := make([]byte, 0, tc.capacity)
			releaseLine(ptr, b)

			kept := cap(*ptr) == tc.capacity
			if kept != tc.wantKept {
				t.Errorf("buffer of cap %d: kept = %v, want %v", tc.capacity, kept, tc.wantKept)
			}
		})
	}
}

// TestWriteTextSurvivesAnEnormousPath is the end-to-end half: the cap must not
// truncate the line it declines to pool, and the sink must keep working after
// dropping a buffer.
func TestWriteTextSurvivesAnEnormousPath(t *testing.T) {
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
		t.Error("the path was not written in full; the cap must bound pooling, not output")
	}

	var next strings.Builder
	writeText(&next, Entry{Start: time.Now(), Method: "GET", Path: "/ok", Status: 200})
	if !strings.Contains(next.String(), "/ok") {
		t.Error("the sink stopped working after dropping an oversized buffer")
	}
}
