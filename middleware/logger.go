package middleware

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/subhanjanops/gomicro"
	"github.com/subhanjanops/loggy"
)

// Entry is one finished request, as the logger middleware sees it.
type Entry struct {
	Start    time.Time
	Latency  time.Duration
	Method   string
	Path     string // the matched route pattern, e.g. "/user/:id"
	URL      string // the requested path, with query string
	Status   int
	Size     int // response bytes, or -1 if nothing was written
	ClientIP string
}

// Level classifies an entry so a Sink can pick a severity without re-deriving
// the rule: 5xx is an error, 4xx a warning, anything else informational.
func (e Entry) Level() slog.Level {
	switch {
	case e.Status >= http.StatusInternalServerError:
		return slog.LevelError
	case e.Status >= http.StatusBadRequest:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

// Sink receives each finished request. It is the seam between this middleware
// and whatever does the logging: implement it — or use one of the adapters
// below — and any logger works, with no dependency in either direction.
//
//	middleware.LoggerWith(middleware.SinkFunc(func(e middleware.Entry) {
//		zlog.Info().Str("path", e.Path).Int("status", e.Status).Send()
//	}))
type Sink interface {
	LogRequest(Entry)
}

// SinkFunc adapts a plain function to Sink.
type SinkFunc func(Entry)

// LogRequest implements Sink.
func (f SinkFunc) LogRequest(e Entry) { f(e) }

// LoggerConfig configures Logger.
type LoggerConfig struct {
	// Sink receives each entry. Defaults to LoggySink(loggy.Default()).
	Sink Sink

	// SkipPaths are request paths that produce no log line — health checks and
	// metrics endpoints, which otherwise drown out everything worth reading.
	SkipPaths []string
}

// Logger returns middleware that logs every request through loggy's default
// logger once the rest of the chain has run.
func Logger() gomicro.HandlerFunc {
	return LoggerWithConfig(LoggerConfig{})
}

// LoggerWith returns a Logger writing through sink.
func LoggerWith(sink Sink) gomicro.HandlerFunc {
	return LoggerWithConfig(LoggerConfig{Sink: sink})
}

// LoggerWithWriter returns a Logger writing plain aligned text to out, with no
// logging library involved.
func LoggerWithWriter(out io.Writer) gomicro.HandlerFunc {
	return LoggerWithConfig(LoggerConfig{Sink: TextSink(out)})
}

// LoggerWithConfig returns a configured Logger.
//
// The entry's Path is the matched route pattern rather than the URL: it has
// bounded cardinality, so logs and metrics group by route instead of by
// customer id. The raw URL is in the same entry when it is genuinely needed.
func LoggerWithConfig(cfg LoggerConfig) gomicro.HandlerFunc {
	sink := cfg.Sink
	if sink == nil {
		sink = LoggySink(loggy.Default())
	}
	// Copied so a later mutation of the caller's slice cannot change behaviour
	// mid-flight. A short slice scan beats a map here: there are a handful of
	// skipped paths at most, and this runs on every request.
	skip := append([]string(nil), cfg.SkipPaths...)

	return func(c *gomicro.Context) {
		path := c.Request.URL.Path
		for _, s := range skip {
			if s == path {
				c.Next()
				return
			}
		}

		start := time.Now()
		c.Next()

		entry := Entry{
			Start:    start,
			Latency:  time.Since(start),
			Method:   c.Request.Method,
			Path:     c.FullPath(),
			URL:      requestURL(c),
			Status:   c.Writer.Status(),
			Size:     c.Writer.Size(),
			ClientIP: c.ClientIP(),
		}
		if entry.Path == "" {
			// No route matched, so there is no pattern; the URL is all there is.
			entry.Path = path
		}

		sink.LogRequest(entry)
	}
}

func requestURL(c *gomicro.Context) string {
	u := c.Request.URL
	if u.RawQuery == "" {
		return u.Path
	}
	return u.Path + "?" + u.RawQuery
}

// ---------------------------------------------------------------- adapters

// LoggySink logs through a loggy.Logger, one structured field per attribute.
// This is the default, via loggy.Default().
func LoggySink(l loggy.Logger) Sink {
	return SinkFunc(func(e Entry) {
		var ev *loggy.Event
		switch e.Level() {
		case slog.LevelError:
			ev = l.Error()
		case slog.LevelWarn:
			ev = l.Warn()
		default:
			ev = l.Info()
		}
		ev.Str("method", e.Method).
			Str("path", e.Path).
			Int("status", e.Status).
			Dur("latency", e.Latency).
			Int("size", e.Size).
			Str("ip", e.ClientIP).
			Msg("request")
	})
}

// SlogSink logs through a *slog.Logger, for services already standardised on
// the standard library.
func SlogSink(l *slog.Logger) Sink {
	return SinkFunc(func(e Entry) {
		l.LogAttrs(context.Background(), e.Level(), "request",
			slog.String("method", e.Method),
			slog.String("path", e.Path),
			slog.Int("status", e.Status),
			slog.Duration("latency", e.Latency),
			slog.Int("size", e.Size),
			slog.String("ip", e.ClientIP),
		)
	})
}

// TextSink writes aligned plain text to out, with no logging library at all:
//
//	[gomicro] 2026/09/07 19:04:11 | 200 |     1.204ms |       127.0.0.1 | GET     /user/:id
//
// It appends into a pooled buffer and never builds a string, so it is the
// cheapest sink here — useful for benchmarks and for services that only want a
// line on stderr.
func TextSink(out io.Writer) Sink {
	if out == nil {
		out = os.Stderr
	}
	return SinkFunc(func(e Entry) { writeText(out, e) })
}

// lineBuffers keeps TextSink from allocating a line per request.
var lineBuffers = sync.Pool{
	New: func() any {
		b := make([]byte, 0, 160)
		return &b
	},
}

func writeText(out io.Writer, e Entry) {
	bufPtr := lineBuffers.Get().(*[]byte)
	b := (*bufPtr)[:0]

	b = append(b, "[gomicro] "...)
	b = e.Start.AppendFormat(b, "2006/01/02 15:04:05")
	b = append(b, " | "...)
	b = strconv.AppendInt(b, int64(e.Status), 10)
	b = append(b, " | "...)
	b = appendPadded(b, e.Latency.String(), 11)
	b = append(b, " | "...)
	b = appendPadded(b, e.ClientIP, 15)
	b = append(b, " | "...)
	b = appendPadded(b, e.Method, 7)
	b = append(b, ' ')
	b = append(b, e.Path...)
	b = append(b, '\n')

	out.Write(b)

	*bufPtr = b
	lineBuffers.Put(bufPtr)
}

// appendPadded right-aligns s in a field of at least width bytes, so columns
// line up in a terminal without reaching for fmt.
func appendPadded(b []byte, s string, width int) []byte {
	for i := len(s); i < width; i++ {
		b = append(b, ' ')
	}
	return append(b, s...)
}
