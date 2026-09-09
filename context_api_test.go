package zarp

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"
)

func ctxFor(method, target string, body string) (*Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	c := &Context{}
	c.reset(rec, r)
	return c, rec
}

// ---------------------------------------------------------------- params

func TestParamAndFullPath(t *testing.T) {
	c, _ := ctxFor("GET", "/user/42", "")
	c.Params = Params{{"id", "42"}, {"tab", "posts"}}
	c.fullPath = "/user/:id/:tab"

	if got := c.Param("id"); got != "42" {
		t.Errorf("Param(id) = %q, want 42", got)
	}
	if got := c.Param("missing"); got != "" {
		t.Errorf("Param(missing) = %q, want empty", got)
	}
	if got := c.FullPath(); got != "/user/:id/:tab" {
		t.Errorf("FullPath = %q", got)
	}
}

// ---------------------------------------------------------------- query

func TestQuery(t *testing.T) {
	c, _ := ctxFor("GET", "/s?q=go&tag=a&tag=b&empty=", "")

	if got := c.Query("q"); got != "go" {
		t.Errorf("Query(q) = %q", got)
	}
	if got := c.Query("nope"); got != "" {
		t.Errorf("Query(nope) = %q, want empty", got)
	}
	if got := c.DefaultQuery("nope", "fallback"); got != "fallback" {
		t.Errorf("DefaultQuery(absent) = %q", got)
	}
	if got := c.DefaultQuery("empty", "fallback"); got != "" {
		t.Errorf("DefaultQuery(present but empty) = %q, want empty not the default", got)
	}
	if v, ok := c.GetQuery("q"); !ok || v != "go" {
		t.Errorf("GetQuery(q) = %q/%v", v, ok)
	}
	if _, ok := c.GetQuery("nope"); ok {
		t.Error("GetQuery(nope) reported present")
	}
	if got := c.QueryArray("tag"); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("QueryArray(tag) = %v", got)
	}
}

func TestQueryParsesOnce(t *testing.T) {
	c, _ := ctxFor("GET", "/s?q=first", "")

	if got := c.Query("q"); got != "first" {
		t.Fatalf("Query = %q", got)
	}
	// Changing the URL afterwards must not be visible: the cache is populated.
	c.Request.URL.RawQuery = "q=second"
	if got := c.Query("q"); got != "first" {
		t.Errorf("Query after cache = %q, want the cached %q", got, "first")
	}

	c.reset(httptest.NewRecorder(), c.Request)
	if c.queryCache != nil {
		t.Error("reset must clear the query cache")
	}
	if got := c.Query("q"); got != "second" {
		t.Errorf("Query after reset = %q, want re-parsed %q", got, "second")
	}
}

// ---------------------------------------------------------------- form

func TestPostForm(t *testing.T) {
	c, _ := ctxFor("POST", "/", "name=octocat&tag=a&tag=b&empty=")
	c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	if got := c.PostForm("name"); got != "octocat" {
		t.Errorf("PostForm(name) = %q", got)
	}
	if got := c.DefaultPostForm("nope", "fallback"); got != "fallback" {
		t.Errorf("DefaultPostForm(absent) = %q", got)
	}
	if got := c.DefaultPostForm("empty", "fallback"); got != "" {
		t.Errorf("DefaultPostForm(present but empty) = %q", got)
	}
	if v, ok := c.GetPostForm("name"); !ok || v != "octocat" {
		t.Errorf("GetPostForm = %q/%v", v, ok)
	}
	if got := c.PostFormArray("tag"); len(got) != 2 {
		t.Errorf("PostFormArray(tag) = %v", got)
	}
}

func TestFormFile(t *testing.T) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("upload", "hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	part.Write([]byte("file contents"))
	w.WriteField("caption", "a caption")
	w.Close()

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/upload", &body)
	r.Header.Set("Content-Type", w.FormDataContentType())
	c := &Context{engine: &Engine{MaxMultipartMemory: 1 << 20}}
	c.reset(rec, r)

	fh, err := c.FormFile("upload")
	if err != nil {
		t.Fatalf("FormFile: %v", err)
	}
	if fh.Filename != "hello.txt" {
		t.Errorf("filename = %q", fh.Filename)
	}
	if got := c.PostForm("caption"); got != "a caption" {
		t.Errorf("PostForm(caption) = %q", got)
	}
	if _, err := c.MultipartForm(); err != nil {
		t.Errorf("MultipartForm: %v", err)
	}
}

func TestFormOnMalformedBodyDoesNotPanic(t *testing.T) {
	c, _ := ctxFor("POST", "/", "%zz=broken")
	c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if got := c.PostForm("anything"); got != "" {
		t.Errorf("PostForm on malformed body = %q, want empty", got)
	}
}

// ---------------------------------------------------------------- keys

func TestKeys(t *testing.T) {
	c, _ := ctxFor("GET", "/", "")

	if c.keys != nil {
		t.Error("keys must be nil before the first Set")
	}
	if _, ok := c.Get("nope"); ok {
		t.Error("Get on a nil map reported present")
	}

	c.Set("user", "octocat")
	c.Set("count", 7)
	c.Set("ok", true)
	c.Set("ratio", 1.5)
	c.Set("id64", int64(9))
	c.Set("ttl", 5*time.Second)

	if v, ok := c.Get("user"); !ok || v != "octocat" {
		t.Errorf("Get(user) = %v/%v", v, ok)
	}
	if got := c.MustGet("count"); got != 7 {
		t.Errorf("MustGet(count) = %v", got)
	}
	if got := c.GetString("user"); got != "octocat" {
		t.Errorf("GetString = %q", got)
	}
	if got := c.GetInt("count"); got != 7 {
		t.Errorf("GetInt = %d", got)
	}
	if got := c.GetInt64("id64"); got != 9 {
		t.Errorf("GetInt64 = %d", got)
	}
	if got := c.GetBool("ok"); !got {
		t.Error("GetBool = false")
	}
	if got := c.GetFloat64("ratio"); got != 1.5 {
		t.Errorf("GetFloat64 = %v", got)
	}
	if got := c.GetDuration("ttl"); got != 5*time.Second {
		t.Errorf("GetDuration = %v", got)
	}
	// A wrong-typed read yields the zero value rather than panicking.
	if got := c.GetInt("user"); got != 0 {
		t.Errorf("GetInt on a string = %d, want 0", got)
	}
}

func TestMustGetPanics(t *testing.T) {
	c, _ := ctxFor("GET", "/", "")
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustGet on a missing key did not panic")
		}
	}()
	c.MustGet("absent")
}

// ---------------------------------------------------------------- context.Context

func TestContextContextCompliance(t *testing.T) {
	var _ context.Context = (*Context)(nil)

	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()

	c, _ := ctxFor("GET", "/", "")
	c.Request = c.Request.WithContext(ctx)

	if _, ok := c.Deadline(); !ok {
		t.Error("Deadline not delegated to the request context")
	}
	if c.Done() == nil {
		t.Error("Done not delegated")
	}
	if c.Err() != nil {
		t.Errorf("Err = %v, want nil", c.Err())
	}
}

func TestContextValueResolutionOrder(t *testing.T) {
	c, _ := ctxFor("GET", "/", "")
	c.Request = c.Request.WithContext(context.WithValue(context.Background(), "src", "request"))
	c.Params = Params{{"id", "from-params"}}
	c.Set("key", "from-keys")

	if got := c.Value("key"); got != "from-keys" {
		t.Errorf("Value(key) = %v, want the Context key", got)
	}
	if got := c.Value("id"); got != "from-params" {
		t.Errorf("Value(id) = %v, want the route param", got)
	}
	if got := c.Value("src"); got != "request" {
		t.Errorf("Value(src) = %v, want the request context value", got)
	}
	if got := c.Value("absent"); got != nil {
		t.Errorf("Value(absent) = %v, want nil", got)
	}
}

func TestNilRequestIsSafe(t *testing.T) {
	c := &Context{}
	if _, ok := c.Deadline(); ok {
		t.Error("Deadline on a nil request")
	}
	if c.Done() != nil || c.Err() != nil || c.Value("x") != nil {
		t.Error("nil request must yield zero values")
	}
	if c.ClientIP() != "" || c.GetHeader("X") != "" || c.ContentType() != "" {
		t.Error("nil request must yield empty strings")
	}
}

// ---------------------------------------------------------------- request info

func TestContentTypeAndWebsocket(t *testing.T) {
	c, _ := ctxFor("POST", "/", "")
	c.Request.Header.Set("Content-Type", "application/json; charset=utf-8")
	if got := c.ContentType(); got != "application/json" {
		t.Errorf("ContentType = %q", got)
	}

	c.Request.Header.Set("Connection", "keep-alive, Upgrade")
	c.Request.Header.Set("Upgrade", "WebSocket")
	if !c.IsWebsocket() {
		t.Error("IsWebsocket = false for an upgrade request")
	}
	c.Request.Header.Set("Upgrade", "h2c")
	if c.IsWebsocket() {
		t.Error("IsWebsocket = true for a non-websocket upgrade")
	}
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		remote     string
		xff, xreal string
		trust      bool
		want       string
	}{
		{"remote addr", "192.0.2.9:1234", "", "", true, "192.0.2.9"},
		{"forwarded first", "10.0.0.1:80", "203.0.113.5, 10.0.0.1", "", true, "203.0.113.5"},
		{"skips junk", "10.0.0.1:80", "not-an-ip, 203.0.113.7", "", true, "203.0.113.7"},
		{"real ip", "10.0.0.1:80", "", "203.0.113.9", true, "203.0.113.9"},
		{"untrusted", "10.0.0.1:80", "203.0.113.5", "", false, "10.0.0.1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := ctxFor("GET", "/", "")
			c.engine = &Engine{ForwardedByClientIP: tc.trust}
			c.Request.RemoteAddr = tc.remote
			if tc.xff != "" {
				c.Request.Header.Set("X-Forwarded-For", tc.xff)
			}
			if tc.xreal != "" {
				c.Request.Header.Set("X-Real-Ip", tc.xreal)
			}
			if got := c.ClientIP(); got != tc.want {
				t.Errorf("ClientIP = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClientIPIgnoresForwardedHeadersByDefault(t *testing.T) {
	c, _ := ctxFor(http.MethodGet, "/", "")
	c.Request.RemoteAddr = "10.0.0.1:80"
	c.Request.Header.Set("X-Forwarded-For", "203.0.113.5")

	// No engine at all, and an engine straight out of New, must both refuse to
	// let the caller name itself.
	if got := c.ClientIP(); got != "10.0.0.1" {
		t.Errorf("ClientIP without an engine = %q, want the peer address", got)
	}
	c.engine = New()
	if got := c.ClientIP(); got != "10.0.0.1" {
		t.Errorf("ClientIP with default config = %q, want the peer address", got)
	}
}

func TestClientIPWithTrustedProxies(t *testing.T) {
	proxies := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("::1/128"),
	}
	tests := []struct {
		name   string
		remote string
		xff    string
		xreal  string
		want   string
	}{
		{
			name:   "peer is not a trusted proxy, so its headers are input",
			remote: "203.0.113.9:443", xff: "198.51.100.1",
			want: "203.0.113.9",
		},
		{
			name:   "single proxy hop",
			remote: "10.0.0.1:80", xff: "203.0.113.5",
			want: "203.0.113.5",
		},
		{
			name:   "walks past our own hops, right to left",
			remote: "10.0.0.1:80", xff: "203.0.113.5, 10.1.2.3, 10.0.0.1",
			want: "203.0.113.5",
		},
		{
			name:   "a spoofed prefix cannot reach past the first untrusted hop",
			remote: "10.0.0.1:80", xff: "1.1.1.1, 203.0.113.5, 10.0.0.1",
			want: "203.0.113.5",
		},
		{
			name:   "every hop trusted falls back to the leftmost",
			remote: "10.0.0.1:80", xff: "10.9.9.9, 10.0.0.1",
			want: "10.9.9.9",
		},
		{
			name:   "junk entries are skipped",
			remote: "10.0.0.1:80", xff: "203.0.113.7, not-an-ip, 10.0.0.1",
			want: "203.0.113.7",
		},
		{
			name:   "X-Real-Ip is used when there is no chain",
			remote: "10.0.0.1:80", xreal: "203.0.113.9",
			want: "203.0.113.9",
		},
		{
			name:   "no usable header falls back to the peer",
			remote: "10.0.0.1:80", xff: "not-an-ip",
			want: "10.0.0.1",
		},
		{
			name:   "IPv4-mapped peer still matches a v4 prefix",
			remote: "[::ffff:10.0.0.1]:80", xff: "203.0.113.5",
			want: "203.0.113.5",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := ctxFor(http.MethodGet, "/", "")
			c.engine = &Engine{ForwardedByClientIP: true, TrustedProxies: proxies}
			c.Request.RemoteAddr = tc.remote
			if tc.xff != "" {
				c.Request.Header.Set("X-Forwarded-For", tc.xff)
			}
			if tc.xreal != "" {
				c.Request.Header.Set("X-Real-Ip", tc.xreal)
			}
			if got := c.ClientIP(); got != tc.want {
				t.Errorf("ClientIP = %q, want %q", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------- response

func TestHeaderSetAndDelete(t *testing.T) {
	c, rec := ctxFor("GET", "/", "")
	c.Header("X-Trace", "abc")
	if got := rec.Header().Get("X-Trace"); got != "abc" {
		t.Errorf("header = %q", got)
	}
	c.Header("X-Trace", "")
	if _, ok := rec.Header()["X-Trace"]; ok {
		t.Error("empty value must delete the header")
	}
}

func TestString(t *testing.T) {
	c, rec := ctxFor("GET", "/", "")
	c.String(http.StatusTeapot, "hello")
	if rec.Code != http.StatusTeapot || rec.Body.String() != "hello" {
		t.Errorf("code=%d body=%q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("content type = %q", ct)
	}
}

func TestText(t *testing.T) {
	c, rec := ctxFor("GET", "/", "")
	c.Text(http.StatusTeapot, "hello")
	if rec.Code != http.StatusTeapot || rec.Body.String() != "hello" {
		t.Errorf("code=%d body=%q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("content type = %q", ct)
	}
}

func TestTextIsNotAFormatString(t *testing.T) {
	c, rec := ctxFor("GET", "/", "")
	// Data with verbs in it must survive untouched — that is the whole point.
	raw := "100%% sure: %s %d %v"
	c.Text(http.StatusOK, raw)
	if got := rec.Body.String(); got != raw {
		t.Errorf("body = %q, want %q verbatim", got, raw)
	}
}

func TestTextKeepsHandlerContentType(t *testing.T) {
	c, rec := ctxFor("GET", "/", "")
	c.Header("Content-Type", "text/csv")
	c.Text(http.StatusOK, "a,b")
	if ct := rec.Header().Get("Content-Type"); ct != "text/csv" {
		t.Errorf("content type = %q, want the handler's choice preserved", ct)
	}
}

func TestStringWithFormat(t *testing.T) {
	c, rec := ctxFor("GET", "/", "")
	c.String(http.StatusOK, "hello %s, you are %d", "octocat", 7)
	if got := rec.Body.String(); got != "hello octocat, you are 7" {
		t.Errorf("body = %q", got)
	}
}

func TestStringKeepsHandlerContentType(t *testing.T) {
	c, rec := ctxFor("GET", "/", "")
	c.Header("Content-Type", "text/csv")
	c.String(http.StatusOK, "a,b")
	if ct := rec.Header().Get("Content-Type"); ct != "text/csv" {
		t.Errorf("content type = %q, want the handler's choice preserved", ct)
	}
}

func TestData(t *testing.T) {
	c, rec := ctxFor("GET", "/", "")
	c.Data(http.StatusOK, "image/png", []byte{0x89, 'P'})
	if rec.Header().Get("Content-Type") != "image/png" || rec.Body.Len() != 2 {
		t.Errorf("content type = %q len = %d", rec.Header().Get("Content-Type"), rec.Body.Len())
	}
}

func TestRedirect(t *testing.T) {
	c, rec := ctxFor("GET", "/", "")
	c.Redirect(http.StatusMovedPermanently, "/elsewhere")
	if rec.Code != http.StatusMovedPermanently {
		t.Errorf("code = %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/elsewhere" {
		t.Errorf("Location = %q", got)
	}
}

func TestRedirectRejectsNonRedirectStatus(t *testing.T) {
	c, _ := ctxFor("GET", "/", "")
	defer func() {
		if r := recover(); r == nil {
			t.Error("Redirect with status 200 did not panic")
		}
	}()
	c.Redirect(http.StatusOK, "/elsewhere")
}

// ---------------------------------------------------------------- JSON

type jsonPayload struct {
	ID    int      `json:"id"`
	Name  string   `json:"name"`
	Tags  []string `json:"tags"`
	Extra string   `json:"extra,omitempty"`
}

func TestJSON(t *testing.T) {
	c, rec := ctxFor("GET", "/", "")
	c.JSON(http.StatusCreated, jsonPayload{ID: 1, Name: "octocat", Tags: []string{"a"}})

	if rec.Code != http.StatusCreated {
		t.Errorf("code = %d", rec.Code)
	}
	want := `{"id":1,"name":"octocat","tags":["a"]}`
	if got := rec.Body.String(); got != want {
		t.Errorf("body = %q, want %q (no trailing newline)", got, want)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("content type = %q", ct)
	}
	// A small body is buffered by net/http, which derives Content-Length
	// itself; setting it here would only cost an allocation.
	if cl := rec.Header().Get("Content-Length"); cl != "" {
		t.Errorf("Content-Length = %q, want it left to net/http for a small body", cl)
	}
}

func TestJSONSetsContentLengthPastChunkingThreshold(t *testing.T) {
	c, rec := ctxFor("GET", "/", "")
	c.JSON(http.StatusOK, jsonPayload{Name: strings.Repeat("x", autoContentLengthLimit)})

	cl := rec.Header().Get("Content-Length")
	if cl == "" {
		t.Fatal("Content-Length not set on a body past the chunking threshold")
	}
	if cl != strconv.Itoa(rec.Body.Len()) {
		t.Errorf("Content-Length = %q, want %d", cl, rec.Body.Len())
	}
}

func TestJSONEncodeErrorDoesNotCommitStatus(t *testing.T) {
	c, rec := ctxFor("GET", "/", "")
	c.JSON(http.StatusOK, make(chan int)) // channels are not encodable

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("code = %d, want 500", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", rec.Body.String())
	}
}

func TestJSONLargePayloadRoundTrips(t *testing.T) {
	payload := make([]jsonPayload, 500)
	for i := range payload {
		payload[i] = jsonPayload{ID: i, Name: strings.Repeat("x", 64)}
	}
	c, rec := ctxFor("GET", "/", "")
	c.JSON(http.StatusOK, payload)

	var back []jsonPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &back); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if len(back) != len(payload) || back[499].ID != 499 {
		t.Errorf("round trip lost data: %d entries", len(back))
	}
}

// ---------------------------------------------------------------- copy

func TestCopyDetaches(t *testing.T) {
	c, _ := ctxFor("GET", "/user/42", "")
	c.Params = append(c.Params, Param{"id", "42"})
	c.Set("user", "octocat")
	c.fullPath = "/user/:id"
	c.engine = New()

	cp := c.Copy()

	if cp.Writer != nil {
		t.Error("a copy must not be able to write")
	}
	if cp.index != abortIndex {
		t.Errorf("index = %d, want abortIndex", cp.index)
	}
	if cp.FullPath() != "/user/:id" || cp.Param("id") != "42" || cp.GetString("user") != "octocat" {
		t.Error("copy lost data")
	}

	// The original returning to the pool must not disturb the copy.
	c.reset(httptest.NewRecorder(), c.Request)
	c.Params = append(c.Params, Param{"id", "OVERWRITTEN"})
	c.Set("user", "someone-else")

	if got := cp.Param("id"); got != "42" {
		t.Errorf("copy param = %q after the original was reused", got)
	}
	if got := cp.GetString("user"); got != "octocat" {
		t.Errorf("copy key = %q after the original was reused", got)
	}
}

// ---------------------------------------------------------------- benchmarks

func BenchmarkContextParam(b *testing.B) {
	c := &Context{Params: Params{{"a", "1"}, {"id", "42"}, {"z", "3"}}}
	b.ReportAllocs()
	for b.Loop() {
		sinkString = c.Param("id")
	}
}

func BenchmarkContextQuery(b *testing.B) {
	c, _ := ctxFor("GET", "/s?q=go&page=2", "")
	c.Query("q") // warm the cache; the benchmark measures cached reads
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		sinkString = c.Query("q")
	}
}

func BenchmarkContextSetGet(b *testing.B) {
	c, _ := ctxFor("GET", "/", "")
	c.Set("user", "octocat")
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		sinkString = c.GetString("user")
	}
}

func BenchmarkContextJSONSmall(b *testing.B) {
	obj := jsonPayload{ID: 1, Name: "octocat", Tags: []string{"a", "b"}}
	rec := httptest.NewRecorder()
	c := &Context{}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		rec.Body.Reset()
		c.reset(rec, nil)
		c.JSON(http.StatusOK, obj)
	}
}

func BenchmarkContextText(b *testing.B) {
	rec := httptest.NewRecorder()
	c := &Context{}
	value := "octocat"
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		rec.Body.Reset()
		c.reset(rec, nil)
		c.Text(http.StatusOK, value)
	}
}

// BenchmarkContextStringFormatted is the same write done through String, which
// is what a handler must otherwise reach for when the text is a variable.
func BenchmarkContextStringFormatted(b *testing.B) {
	rec := httptest.NewRecorder()
	c := &Context{}
	value := "octocat"
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		rec.Body.Reset()
		c.reset(rec, nil)
		c.String(http.StatusOK, "%s", value)
	}
}

func BenchmarkContextString(b *testing.B) {
	rec := httptest.NewRecorder()
	c := &Context{}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		rec.Body.Reset()
		c.reset(rec, nil)
		c.String(http.StatusOK, "pong")
	}
}

var sinkString string

func TestResponseWriterUnwrap(t *testing.T) {
	rec := httptest.NewRecorder()
	c := &Context{}
	c.reset(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if got := c.Writer.Unwrap(); got != http.ResponseWriter(rec) {
		t.Errorf("Unwrap returned %T, want the writer net/http handed us", got)
	}

	// The point of Unwrap: http.NewResponseController can find the real writer
	// through the wrapper, so deadlines and flushing work.
	if err := http.NewResponseController(c.Writer).Flush(); err != nil {
		t.Errorf("ResponseController.Flush through the wrapper: %v", err)
	}
}

func TestFormErrorDistinguishesAbsentFromMalformed(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		contentType string
		wantErr     bool
	}{
		{"well formed", "name=octocat&role=admin", "application/x-www-form-urlencoded", false},
		{"field absent", "role=admin", "application/x-www-form-urlencoded", false},
		{"empty body", "", "application/x-www-form-urlencoded", false},
		// Go rejects a semicolon separator, and %zz is not an escape.
		{"semicolon separator", "name=a;role=b", "application/x-www-form-urlencoded", true},
		{"bad escape", "name=%zz", "application/x-www-form-urlencoded", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := ctxFor(http.MethodPost, "/x", tc.body)
			c.Request.Header.Set("Content-Type", tc.contentType)

			name := c.PostForm("name")
			err := c.FormError()

			switch {
			case tc.wantErr && err == nil:
				t.Errorf("malformed body reported no error; PostForm gave %q", name)
			case !tc.wantErr && err != nil:
				t.Errorf("well-formed body reported %v", err)
			}
		})
	}
}

func TestFormErrorIsClearedByReset(t *testing.T) {
	rec := httptest.NewRecorder()
	c := &Context{}

	bad := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("name=%zz"))
	bad.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.reset(rec, bad)
	if c.FormError() == nil {
		t.Fatal("malformed body reported no error")
	}

	// The Context goes back to the pool and must not carry the failure into
	// the next request.
	good := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("name=ok"))
	good.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.reset(rec, good)
	if err := c.FormError(); err != nil {
		t.Errorf("error survived reset: %v", err)
	}
}

func TestRedirectAcceptsOnly3xx(t *testing.T) {
	for _, code := range []int{300, 301, 302, 303, 307, 308} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			c, rec := ctxFor(http.MethodGet, "/", "")
			c.Redirect(code, "/elsewhere")

			if rec.Code != code {
				t.Errorf("code = %d, want %d", rec.Code, code)
			}
			if got := rec.Header().Get("Location"); got != "/elsewhere" {
				t.Errorf("Location = %q", got)
			}
		})
	}

	// 201 is not a redirect. A created resource carries Location alongside its
	// own body, which is a different thing entirely.
	for _, code := range []int{200, 201, 204, 299, 400, 500} {
		t.Run("rejects "+strconv.Itoa(code), func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("Redirect(%d) did not panic", code)
				}
			}()
			c, _ := ctxFor(http.MethodGet, "/", "")
			c.Redirect(code, "/elsewhere")
		})
	}
}
