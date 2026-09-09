package zarp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// noWritten marks a response that has not written a byte yet, which is
	// distinct from one that wrote zero bytes.
	noWritten = -1

	defaultStatus = http.StatusOK
)

// ResponseWriter is the http.ResponseWriter handlers are given. Beyond the
// standard interface it reports what has been written — which is how middleware
// logs a status and size it never set itself — and it defers the status until
// the first write, so a later handler in the chain can still change it.
//
// Flusher and Hijacker are part of the interface rather than an optional
// assertion, because streaming responses and protocol upgrades break silently
// when a wrapper forgets to forward them.
//
// Unwrap returns the writer net/http handed us. It is what
// http.NewResponseController looks for, so read and write deadlines work
// through this wrapper, and it is what to pass to a net/http function that
// needs to talk to the real response — http.MaxBytesReader, which signals the
// server through a method only net/http's own writer has.
type ResponseWriter interface {
	http.ResponseWriter
	http.Flusher
	http.Hijacker
	Status() int
	Size() int
	Written() bool
	WriteHeaderNow()
	Unwrap() http.ResponseWriter
}

var (
	// A Context is passed anywhere a context.Context is expected — to a database
	// driver, an outbound request, anything taking one — so losing a method of
	// that interface has to fail here rather than at a call site.
	_ context.Context = (*Context)(nil)

	// The pooled writer has to keep satisfying the interface handlers see.
	_ ResponseWriter = (*responseWriter)(nil)
)

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

// Unwrap returns the underlying writer, for http.NewResponseController and for
// net/http helpers that type-assert on the concrete response.
func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

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
		return nil, nil, errors.New("zarp: the ResponseWriter does not implement http.Hijacker")
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
	formCache  url.Values
	formErr    error
	keys       map[string]any
	engine     *Engine
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
	c.Params = c.Params[:0] // keep the backing array sized by router.maxParams
	c.handlers = nil
	c.queryCache = nil
	c.formCache = nil
	c.formErr = nil
	c.keys = nil
	c.fullPath = ""
	c.index = -1
}

// ---------------------------------------------------------------- engine hooks

// defaultMultipartMemory caps how much of a multipart body is buffered in
// memory before spilling to temporary files.
const defaultMultipartMemory = 32 << 20 // 32 MB

// maxMultipartMemory falls back to the package default for a Context built
// without an Engine, which is how the unit tests construct one.
func (c *Context) maxMultipartMemory() int64 {
	if c.engine != nil && c.engine.MaxMultipartMemory > 0 {
		return c.engine.MaxMultipartMemory
	}
	return defaultMultipartMemory
}

// ---------------------------------------------------------------- params

// Param returns the value of the URL parameter named key, or "" if the matched
// route has no such parameter.
func (c *Context) Param(key string) string {
	return c.Params.ByName(key)
}

// FullPath returns the matched route pattern, e.g. "/user/:id" — not the
// request URL. Log this rather than the URL: it has bounded cardinality.
func (c *Context) FullPath() string {
	return c.fullPath
}

// ---------------------------------------------------------------- query

// initQueryCache parses the query string once per request. url.Values costs a
// map allocation, so a handler reading three parameters should pay for it once,
// not three times.
func (c *Context) initQueryCache() {
	if c.queryCache != nil {
		return
	}
	if c.Request == nil || c.Request.URL == nil {
		c.queryCache = url.Values{}
		return
	}
	c.queryCache = c.Request.URL.Query()
}

// Query returns the first value for key, or "".
func (c *Context) Query(key string) string {
	v, _ := c.GetQuery(key)
	return v
}

// DefaultQuery returns the first value for key, or def when it is absent. A
// present but empty parameter ("?x=") returns "", not def.
func (c *Context) DefaultQuery(key, def string) string {
	if v, ok := c.GetQuery(key); ok {
		return v
	}
	return def
}

// GetQuery returns the first value for key and whether it was present at all.
func (c *Context) GetQuery(key string) (string, bool) {
	values, ok := c.GetQueryArray(key)
	if !ok {
		return "", false
	}
	return values[0], true
}

// QueryArray returns every value for key.
func (c *Context) QueryArray(key string) []string {
	values, _ := c.GetQueryArray(key)
	return values
}

// GetQueryArray returns every value for key and whether key was present.
func (c *Context) GetQueryArray(key string) ([]string, bool) {
	c.initQueryCache()
	values, ok := c.queryCache[key]
	if !ok || len(values) == 0 {
		return nil, false
	}
	return values, true
}

// ---------------------------------------------------------------- form

// multipartContentType is the only body type ParseMultipartForm is asked to
// handle; anything else goes through ParseForm alone.
const multipartContentType = "multipart/form-data"

// initFormCache parses the request body once. ParseMultipartForm handles both
// urlencoded and multipart bodies, so a non-multipart body is not an error.
//
// A parse failure is recorded rather than raised: the accessors return zero
// values, as they do for a field that simply was not sent, and FormError says
// which of the two happened.
func (c *Context) initFormCache() {
	if c.formCache != nil {
		return
	}
	c.formCache = url.Values{}
	if c.Request == nil {
		return
	}

	// ParseForm first, and only then ParseMultipartForm, because the reverse
	// order loses the error. ParseMultipartForm calls ParseForm itself, and if
	// the body turns out not to be multipart it returns ErrNotMultipart and
	// drops whatever ParseForm made of it — a malformed urlencoded body then
	// looks like a request that simply carried no fields. Asking again does not
	// help: ParseForm caches its result and reports no error the second time.
	if err := c.Request.ParseForm(); err != nil {
		c.formErr = err
	}
	// EqualFold: a media type's type and subtype are case-insensitive, and a
	// client sending "Multipart/Form-Data" would otherwise skip the multipart
	// parse entirely and see every field come back empty.
	if strings.EqualFold(c.ContentType(), multipartContentType) {
		if err := c.Request.ParseMultipartForm(c.maxMultipartMemory()); err != nil &&
			!errors.Is(err, http.ErrNotMultipart) {
			c.formErr = err
		}
	}

	// Whatever parsed before a failure is still served; formErr says the rest
	// is missing.
	if c.Request.PostForm != nil {
		c.formCache = c.Request.PostForm
	}
}

// FormError reports why parsing the request body failed, or nil.
//
// The form accessors cannot return an error — PostForm("name") has nowhere to
// put one — so a malformed, oversized or truncated body would otherwise look
// exactly like a request that left every field out. That difference matters:
// one is a client that sent nothing, the other is a client whose body was
// rejected, and only the second should be answered with 400 or 413.
//
//	name := c.PostForm("name")
//	if err := c.FormError(); err != nil {
//		c.AbortWithStatusJSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
//		return
//	}
//
// It reports on the parse the accessors performed, so call it after reading at
// least one field. binding.Form and binding.Multipart check it for you.
func (c *Context) FormError() error {
	c.initFormCache()
	return c.formErr
}

// PostForm returns the first form value for key, or "".
func (c *Context) PostForm(key string) string {
	v, _ := c.GetPostForm(key)
	return v
}

// DefaultPostForm returns the first form value for key, or def when absent.
func (c *Context) DefaultPostForm(key, def string) string {
	if v, ok := c.GetPostForm(key); ok {
		return v
	}
	return def
}

// GetPostForm returns the first form value for key and whether it was present.
func (c *Context) GetPostForm(key string) (string, bool) {
	values, ok := c.GetPostFormArray(key)
	if !ok {
		return "", false
	}
	return values[0], true
}

// PostFormArray returns every form value for key.
func (c *Context) PostFormArray(key string) []string {
	values, _ := c.GetPostFormArray(key)
	return values
}

// GetPostFormArray returns every form value for key and whether key was present.
func (c *Context) GetPostFormArray(key string) ([]string, bool) {
	c.initFormCache()
	values, ok := c.formCache[key]
	if !ok || len(values) == 0 {
		return nil, false
	}
	return values, true
}

// FormFile returns the header of the first file uploaded under name.
func (c *Context) FormFile(name string) (*multipart.FileHeader, error) {
	if c.Request.MultipartForm == nil {
		if err := c.Request.ParseMultipartForm(c.maxMultipartMemory()); err != nil {
			return nil, err
		}
	}
	f, fh, err := c.Request.FormFile(name)
	if err != nil {
		return nil, err
	}
	// The header is what the caller wanted; the open file is not.
	_ = f.Close()
	return fh, nil
}

// MultipartForm parses and returns the whole multipart form.
func (c *Context) MultipartForm() (*multipart.Form, error) {
	err := c.Request.ParseMultipartForm(c.maxMultipartMemory())
	return c.Request.MultipartForm, err
}

// ---------------------------------------------------------------- keys

// Set stores a value on this request, allocating the key map on first use: a
// request that never calls Set never pays for it.
func (c *Context) Set(key string, value any) {
	if c.keys == nil {
		c.keys = make(map[string]any)
	}
	c.keys[key] = value
}

// Get returns the value stored under key and whether it exists.
func (c *Context) Get(key string) (any, bool) {
	v, ok := c.keys[key]
	return v, ok
}

// MustGet returns the value stored under key, panicking if it is absent.
func (c *Context) MustGet(key string) any {
	if v, ok := c.Get(key); ok {
		return v
	}
	panic("zarp: key " + strconv.Quote(key) + " does not exist")
}

// GetString returns the value stored under key as a string, or "".
func (c *Context) GetString(key string) string {
	v, _ := c.Get(key)
	s, _ := v.(string)
	return s
}

// GetBool returns the value stored under key as a bool, or false.
func (c *Context) GetBool(key string) bool {
	v, _ := c.Get(key)
	b, _ := v.(bool)
	return b
}

// GetInt returns the value stored under key as an int, or 0.
func (c *Context) GetInt(key string) int {
	v, _ := c.Get(key)
	i, _ := v.(int)
	return i
}

// GetInt64 returns the value stored under key as an int64, or 0.
func (c *Context) GetInt64(key string) int64 {
	v, _ := c.Get(key)
	i, _ := v.(int64)
	return i
}

// GetFloat64 returns the value stored under key as a float64, or 0.
func (c *Context) GetFloat64(key string) float64 {
	v, _ := c.Get(key)
	f, _ := v.(float64)
	return f
}

// GetDuration returns the value stored under key as a time.Duration, or 0.
func (c *Context) GetDuration(key string) time.Duration {
	v, _ := c.Get(key)
	d, _ := v.(time.Duration)
	return d
}

// ---------------------------------------------------------------- context.Context

// Deadline implements context.Context, delegating to the request's context.
func (c *Context) Deadline() (time.Time, bool) {
	if c.Request == nil {
		return time.Time{}, false
	}
	return c.Request.Context().Deadline()
}

// Done implements context.Context.
func (c *Context) Done() <-chan struct{} {
	if c.Request == nil {
		return nil
	}
	return c.Request.Context().Done()
}

// Err implements context.Context.
func (c *Context) Err() error {
	if c.Request == nil {
		return nil
	}
	return c.Request.Context().Err()
}

// Value implements context.Context. Keys set on this Context, then route
// params, resolve first, so library code holding only a context.Context can
// still read what middleware stored.
func (c *Context) Value(key any) any {
	if k, ok := key.(string); ok {
		if v, exists := c.keys[k]; exists {
			return v
		}
		if v, exists := c.Params.Get(k); exists {
			return v
		}
	}
	if c.Request == nil {
		return nil
	}
	return c.Request.Context().Value(key)
}

// ---------------------------------------------------------------- request info

func (c *Context) requestHeader(key string) string {
	if c.Request == nil {
		return ""
	}
	return c.Request.Header.Get(key)
}

// GetHeader returns a request header.
func (c *Context) GetHeader(key string) string {
	return c.requestHeader(key)
}

// ContentType returns the request media type with any parameters stripped.
func (c *Context) ContentType() string {
	ct := c.requestHeader("Content-Type")
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.TrimSpace(ct)
}

// IsWebsocket reports whether the request is a WebSocket upgrade.
func (c *Context) IsWebsocket() bool {
	return strings.Contains(strings.ToLower(c.requestHeader("Connection")), "upgrade") &&
		strings.EqualFold(c.requestHeader("Upgrade"), "websocket")
}

// ClientIP returns the client's network address.
//
// By default that is the peer address from RemoteAddr, and the X-Forwarded-For
// and X-Real-Ip headers are ignored: they are set by whoever connects, so
// believing them on a directly reachable server lets a caller choose its own
// identity for your rate limits, allowlists and audit logs.
//
// Set Engine.ForwardedByClientIP when the server is only ever reached through a
// proxy, and list the proxies in Engine.TrustedProxies so the headers are
// believed only when the peer is one of them.
func (c *Context) ClientIP() string {
	remote := c.remoteIP()
	if !c.trustForwardedFor() {
		return remote
	}

	trusted := c.trustedProxies()
	if len(trusted) > 0 && !containsIP(trusted, remote) {
		// The peer is not a proxy we configured, so its headers are ordinary
		// user input.
		return remote
	}

	if ip := forwardedFor(c.requestHeader("X-Forwarded-For"), trusted); ip != "" {
		return ip
	}
	if ip := firstValidIP(c.requestHeader("X-Real-Ip")); ip != "" {
		return ip
	}
	return remote
}

// remoteIP is the peer address with any port removed. It is the one address in
// a request that a client cannot choose for itself.
func (c *Context) remoteIP() string {
	if c.Request == nil {
		return ""
	}
	addr := strings.TrimSpace(c.Request.RemoteAddr)
	if ip, _, err := net.SplitHostPort(addr); err == nil {
		return ip
	}
	return addr
}

// trustForwardedFor defaults to false without an Engine, matching the flag's
// default in New.
func (c *Context) trustForwardedFor() bool {
	return c.engine != nil && c.engine.ForwardedByClientIP
}

func (c *Context) trustedProxies() []netip.Prefix {
	if c.engine == nil {
		return nil
	}
	return c.engine.TrustedProxies
}

// forwardedFor picks the client out of an X-Forwarded-For chain.
//
// With trusted proxies configured it walks right to left — from the hop that
// talked to us towards the origin — and returns the first address that is not
// one of ours: everything to the left of that is written by a machine we have
// no reason to believe. With no proxies configured the caller has said the
// headers are trustworthy, so the leftmost address is taken at face value.
func forwardedFor(header string, trusted []netip.Prefix) string {
	if header == "" {
		return ""
	}
	if len(trusted) == 0 {
		return firstValidIP(header)
	}

	leftmost := ""
	for rest := header; len(rest) > 0; {
		var part string
		if i := strings.LastIndexByte(rest, ','); i >= 0 {
			part, rest = rest[i+1:], rest[:i]
		} else {
			part, rest = rest, ""
		}

		ip := strings.TrimSpace(part)
		if !validIP(ip) {
			continue
		}
		leftmost = ip
		if !containsIP(trusted, ip) {
			return ip
		}
	}
	// Every hop was a proxy of ours, so the leftmost is as far back as the
	// chain goes.
	return leftmost
}

// firstValidIP returns the first parseable address in a comma-separated header.
func firstValidIP(header string) string {
	for len(header) > 0 {
		// Cut hands back the whole string and an empty remainder when there is
		// no comma left, which is exactly the last-entry case.
		part, rest, _ := strings.Cut(header, ",")
		header = rest
		if ip := strings.TrimSpace(part); validIP(ip) {
			return ip
		}
	}
	return ""
}

func validIP(s string) bool {
	_, err := netip.ParseAddr(s)
	return err == nil
}

// containsIP reports whether s parses and falls inside any of the prefixes.
// IPv4-mapped IPv6 addresses are unmapped first, so a v4 prefix matches the
// v4-mapped form a dual-stack listener reports.
func containsIP(prefixes []netip.Prefix, s string) bool {
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return false
	}
	addr = addr.Unmap().WithZone("")
	for _, p := range prefixes {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- response

// Status records the response status. It is not sent until the first write or
// WriteHeaderNow, so a later handler in the chain can still change it.
func (c *Context) Status(code int) {
	c.Writer.WriteHeader(code)
}

// Header sets a response header, or removes it when value is empty.
func (c *Context) Header(key, value string) {
	if value == "" {
		c.Writer.Header().Del(key)
		return
	}
	c.Writer.Header().Set(key, value)
}

// writeContentType sets the content type unless the handler already chose one.
func (c *Context) writeContentType(value string) {
	header := c.Writer.Header()
	if len(header["Content-Type"]) == 0 {
		header["Content-Type"] = []string{value}
	}
}

// Text writes a plain-text response verbatim.
//
// Unlike String, s is data rather than a format template: nothing in it is
// interpreted, no fmt machinery runs, and go vet has no non-constant format
// string to report. Prefer it whenever the text comes from a variable.
func (c *Context) Text(code int, s string) {
	c.Status(code)
	c.writeContentType("text/plain; charset=utf-8")
	_, _ = c.writermem.WriteString(s)
}

// String writes a formatted plain-text response. With no values it writes
// format as it is, so the common case never reaches fmt.
//
// format is a template, so passing a variable to it makes go vet report a
// non-constant format string. That call is safe here — with no values the
// string is written verbatim — but use Text for variable text and the warning
// goes away along with the fmt allocation.
func (c *Context) String(code int, format string, values ...any) {
	c.Status(code)
	c.writeContentType("text/plain; charset=utf-8")
	if len(values) == 0 {
		_, _ = c.writermem.WriteString(format)
		return
	}
	fmt.Fprintf(c.Writer, format, values...)
}

// Data writes raw bytes with an explicit content type.
func (c *Context) Data(code int, contentType string, data []byte) {
	c.Status(code)
	c.writeContentType(contentType)
	_, _ = c.Writer.Write(data)
}

// Redirect sends an HTTP redirect: a 3xx status and a Location header.
//
// It panics on anything outside 300-308. In particular it does not accept 201,
// which is not a redirect: a created resource is answered with the response
// body its creator wants, and the Location header set alongside it —
//
//	c.Header("Location", "/users/"+id)
//	c.JSON(http.StatusCreated, user)
//
// — rather than through a function whose whole job is to send no body.
func (c *Context) Redirect(code int, location string) {
	if code < http.StatusMultipleChoices || code > http.StatusPermanentRedirect {
		panic("zarp: cannot redirect with status code " + strconv.Itoa(code))
	}
	c.Header("Location", location)
	c.Status(code)
	c.Writer.WriteHeaderNow()
}

// jsonBuffers backs JSON's encoding. Encoding into a pooled buffer rather than
// straight to the writer costs about 8% on small payloads and buys two things
// the streaming encoder cannot: a failed encode never commits a status code,
// and the response carries a real Content-Length instead of being chunked.
var jsonBuffers = sync.Pool{New: func() any { return new(bytes.Buffer) }}

// maxPooledBuffer stops one huge response from pinning a huge buffer forever.
const maxPooledBuffer = 64 << 10

// autoContentLengthLimit mirrors net/http bufferBeforeChunkingSize: at or below
// it the standard library sets Content-Length for us.
const autoContentLengthLimit = 2048

// JSON serialises obj and writes it with the given status code.
func (c *Context) JSON(code int, obj any) {
	buf := jsonBuffers.Get().(*bytes.Buffer)
	buf.Reset()

	if err := json.NewEncoder(buf).Encode(obj); err != nil {
		releaseBuffer(buf)
		// Nothing is on the wire yet, so the status can still be honest.
		c.Status(http.StatusInternalServerError)
		c.Writer.WriteHeaderNow()
		return
	}

	// Encoder appends a newline; drop it so the body is exactly the value.
	b := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))

	c.Status(code)
	c.writeContentType("application/json; charset=utf-8")
	// net/http buffers small responses and derives Content-Length itself; only
	// a body past that buffer would otherwise be sent chunked. Setting the
	// header unconditionally costs an allocation in textproto.MIMEHeader.Set
	// for no gain on the common path.
	if len(b) > autoContentLengthLimit {
		c.Writer.Header().Set("Content-Length", strconv.Itoa(len(b)))
	}
	_, _ = c.Writer.Write(b)
	releaseBuffer(buf)
}

func releaseBuffer(buf *bytes.Buffer) {
	if buf.Cap() <= maxPooledBuffer {
		jsonBuffers.Put(buf)
	}
}

// ---------------------------------------------------------------- copy

// Copy returns a Context whose metadata outlives the handler, for work that
// continues in a goroutine. The original goes back to the pool the moment the
// handler returns and is overwritten by the next request, so anything reading
// it later must read a copy instead.
//
// What is detached: the params and the key/value store are copied, so the
// original can be reset without disturbing them. The copy cannot write a
// response — its Writer is nil and its chain is spent — which is deliberate:
// the response is finished by the time the copy is used.
//
// What is not detached is Request. It is the same *http.Request, and net/http
// closes its Body once the handler returns, so a goroutine holding a copy must
// not read the body:
//
//	body, _ := io.ReadAll(c.Request.Body) // read it here, in the handler
//	cp := c.Copy()
//	go audit(cp, body)                    // not in there
//
// Reading the request's method, URL, headers and context from a copy is fine —
// net/http does not reuse request objects between requests, so nothing else
// will write to them. Mutating them from the goroutine is not: the handler's
// own goroutine may still be running.
//
// The copy's context is still the request's, and net/http cancels that when the
// response is finished. Work that must outlive the response therefore needs a
// context of its own — context.WithoutCancel, or a fresh one with its own
// deadline — because a copy is a snapshot of the request, not an extension of
// its lifetime.
func (c *Context) Copy() *Context {
	cp := &Context{
		Request:  c.Request,
		engine:   c.engine,
		fullPath: c.fullPath,
		index:    abortIndex,
	}
	if len(c.Params) > 0 {
		cp.Params = make(Params, len(c.Params))
		copy(cp.Params, c.Params)
	}
	if len(c.keys) > 0 {
		cp.keys = maps.Clone(c.keys)
	}
	return cp
}
