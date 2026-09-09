package binding_test

import (
	"bytes"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gozarp/zarp"
	"github.com/gozarp/zarp/binding"
)

// run registers a handler that binds, and returns whatever it recorded.
func run(t *testing.T, method, target, body, contentType string, bind func(*zarp.Context) error) (*httptest.ResponseRecorder, error) {
	t.Helper()
	var bindErr error

	e := zarp.New()
	e.Handle(method, "/u/:id", func(c *zarp.Context) {
		bindErr = bind(c)
	})

	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, r)
	return rec, bindErr
}

// ---------------------------------------------------------------- JSON

type user struct {
	Name  string   `json:"name"`
	Age   int      `json:"age"`
	Tags  []string `json:"tags"`
	Admin bool     `json:"admin"`
}

func TestJSON(t *testing.T) {
	var got user
	_, err := run(t, "POST", "/u/1", `{"name":"octocat","age":7,"tags":["a","b"],"admin":true}`,
		binding.MIMEJSON, func(c *zarp.Context) error { return binding.JSON(c, &got) })

	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if got.Name != "octocat" || got.Age != 7 || len(got.Tags) != 2 || !got.Admin {
		t.Errorf("got %+v", got)
	}
}

func TestJSONEmptyBody(t *testing.T) {
	var got user
	_, err := run(t, "POST", "/u/1", "", binding.MIMEJSON,
		func(c *zarp.Context) error { return binding.JSON(c, &got) })

	if !errors.Is(err, binding.ErrEmptyBody) {
		t.Errorf("err = %v, want ErrEmptyBody", err)
	}
}

func TestJSONMalformed(t *testing.T) {
	var got user
	_, err := run(t, "POST", "/u/1", `{"name":`, binding.MIMEJSON,
		func(c *zarp.Context) error { return binding.JSON(c, &got) })

	if err == nil {
		t.Fatal("want an error")
	}
	if errors.Is(err, binding.ErrEmptyBody) {
		t.Error("a truncated body is not an empty one")
	}
}

func TestJSONBodyCap(t *testing.T) {
	small := binding.JSONWith(binding.JSONConfig{MaxBodyBytes: 32})

	var got user
	_, err := run(t, "POST", "/u/1", `{"name":"`+strings.Repeat("x", 200)+`"}`, binding.MIMEJSON,
		func(c *zarp.Context) error { return binding.With(c, &got, small) })

	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("err = %v, want a body-size error", err)
	}
}

func TestJSONDisallowUnknownFields(t *testing.T) {
	strict := binding.JSONWith(binding.JSONConfig{DisallowUnknownFields: true})

	var got user
	_, err := run(t, "POST", "/u/1", `{"name":"x","nope":1}`, binding.MIMEJSON,
		func(c *zarp.Context) error { return binding.With(c, &got, strict) })

	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("err = %v, want it to name the unknown field", err)
	}
}

// ---------------------------------------------------------------- query / form

type search struct {
	Q       string        `form:"q"`
	Page    int           `form:"page"`
	Tags    []string      `form:"tag"`
	Ratio   float64       `form:"ratio"`
	Live    bool          `form:"live"`
	Timeout time.Duration `form:"timeout"`
	Limit   *int          `form:"limit"`
	Missing string        `form:"missing"`
	Skipped string        `form:"-"`
	ByName  string
}

func TestQuery(t *testing.T) {
	var got search
	target := "/u/1?q=go&page=2&tag=a&tag=b&ratio=1.5&live=true&timeout=3s&limit=10&ByName=named"
	_, err := run(t, "GET", target, "", "",
		func(c *zarp.Context) error { return bindAndValidateQuery(c, &got) })

	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	switch {
	case got.Q != "go", got.Page != 2, got.Ratio != 1.5, !got.Live:
		t.Errorf("scalars: %+v", got)
	}
	if len(got.Tags) != 2 || got.Tags[1] != "b" {
		t.Errorf("slice: %v", got.Tags)
	}
	if got.Timeout != 3*time.Second {
		t.Errorf("duration: %v", got.Timeout)
	}
	if got.Limit == nil || *got.Limit != 10 {
		t.Errorf("pointer: %v", got.Limit)
	}
	if got.Missing != "" {
		t.Errorf("absent key should stay zero, got %q", got.Missing)
	}
	if got.ByName != "named" {
		t.Errorf("untagged field should match by name, got %q", got.ByName)
	}
}

func TestQuerySkipsDashTag(t *testing.T) {
	var got search
	_, err := run(t, "GET", "/u/1?-=nope&Skipped=nope", "", "",
		func(c *zarp.Context) error { return bindAndValidateQuery(c, &got) })
	if err != nil {
		t.Fatal(err)
	}
	if got.Skipped != "" {
		t.Errorf(`field tagged "-" was bound: %q`, got.Skipped)
	}
}

func TestQueryBadValue(t *testing.T) {
	var got search
	_, err := run(t, "GET", "/u/1?page=abc", "", "",
		func(c *zarp.Context) error { return bindAndValidateQuery(c, &got) })

	if err == nil || !strings.Contains(err.Error(), "Page") {
		t.Errorf("err = %v, want it to name the field", err)
	}
}

func TestForm(t *testing.T) {
	var got search
	_, err := run(t, "POST", "/u/1", "q=go&page=3", binding.MIMEPOSTForm,
		func(c *zarp.Context) error { return binding.Form(c, &got) })

	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if got.Q != "go" || got.Page != 3 {
		t.Errorf("got %+v", got)
	}
}

func TestMultipart(t *testing.T) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	w.WriteField("q", "go")
	w.WriteField("page", "4")
	part, _ := w.CreateFormFile("upload", "a.txt")
	part.Write([]byte("data"))
	w.Close()

	var got search
	var fileName string
	e := zarp.New()
	e.POST("/upload", func(c *zarp.Context) {
		if err := binding.Multipart(c, &got); err != nil {
			t.Errorf("bind: %v", err)
		}
		if fh, err := c.FormFile("upload"); err == nil {
			fileName = fh.Filename
		}
	})

	r := httptest.NewRequest(http.MethodPost, "/upload", &body)
	r.Header.Set("Content-Type", w.FormDataContentType())
	e.ServeHTTP(httptest.NewRecorder(), r)

	if got.Q != "go" || got.Page != 4 {
		t.Errorf("values: %+v", got)
	}
	if fileName != "a.txt" {
		t.Errorf("file: %q — files are read with FormFile, not bound", fileName)
	}
}

// ---------------------------------------------------------------- uri / header

func TestURI(t *testing.T) {
	type params struct {
		ID   int    `uri:"id"`
		Slug string `uri:"slug"`
	}
	var got params

	e := zarp.New()
	e.GET("/u/:id/:slug", func(c *zarp.Context) {
		if err := binding.URI(c, &got); err != nil {
			t.Errorf("bind: %v", err)
		}
	})
	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/u/42/hello", nil))

	if got.ID != 42 || got.Slug != "hello" {
		t.Errorf("got %+v", got)
	}
}

func TestHeader(t *testing.T) {
	type headers struct {
		RequestID string `header:"x-request-id"` // canonicalised on lookup
		Agent     string `header:"User-Agent"`
	}
	var got headers

	e := zarp.New()
	e.GET("/h", func(c *zarp.Context) {
		if err := binding.Header(c, &got); err != nil {
			t.Errorf("bind: %v", err)
		}
	})
	r := httptest.NewRequest(http.MethodGet, "/h", nil)
	r.Header.Set("X-Request-Id", "abc123")
	r.Header.Set("User-Agent", "test")
	e.ServeHTTP(httptest.NewRecorder(), r)

	if got.RequestID != "abc123" || got.Agent != "test" {
		t.Errorf("got %+v", got)
	}
}

// ---------------------------------------------------------------- Default / Bind

func TestDefault(t *testing.T) {
	tests := []struct {
		method, contentType, want string
	}{
		{"GET", "", "query"},
		{"DELETE", binding.MIMEJSON, "query"},
		{"POST", binding.MIMEJSON, "json"},
		{"POST", "application/json; charset=utf-8", "json"},
		{"PUT", binding.MIMEPOSTForm, "form"},
		{"POST", "multipart/form-data; boundary=x", "multipart"},
		{"POST", "text/plain", "unsupported"},
		{"POST", "", "unsupported"},
	}
	for _, tc := range tests {
		if got := binding.Default(tc.method, tc.contentType).Name(); got != tc.want {
			t.Errorf("Default(%s, %q) = %s, want %s", tc.method, tc.contentType, got, tc.want)
		}
	}
}

func TestBindPicksByContentType(t *testing.T) {
	var got user
	_, err := run(t, "POST", "/u/1", `{"name":"via-bind"}`, binding.MIMEJSON,
		func(c *zarp.Context) error { return binding.Bind(c, &got) })

	if err != nil || got.Name != "via-bind" {
		t.Errorf("err=%v got=%+v", err, got)
	}
}

func TestBindRejectsNonPointer(t *testing.T) {
	var got search
	_, err := run(t, "GET", "/u/1?q=x", "", "",
		func(c *zarp.Context) error { return binding.Query(c, got) })

	if err == nil || !strings.Contains(err.Error(), "pointer") {
		t.Errorf("err = %v, want a pointer complaint", err)
	}
}

// ---------------------------------------------------------------- validation

type account struct {
	Name  string `form:"name"  binding:"required,min=2,max=8"`
	Email string `form:"email" binding:"required,email"`
	Role  string `form:"role"  binding:"oneof=admin member"`
	Age   int    `form:"age"   binding:"min=18"`
	Code  string `form:"code"  binding:"len=4"`
	Note  string `form:"note"` // no rules
}

// bindAndValidateQuery is the two explicit steps the package now asks for:
// binding no longer validates on its own.
func bindAndValidateQuery(c *zarp.Context, obj any) error {
	if err := binding.Query(c, obj); err != nil {
		return err
	}
	return binding.Validate(obj)
}

func valid() string {
	return "name=octo&email=o@example.com&role=admin&age=30&code=abcd"
}

func TestValidateAccepts(t *testing.T) {
	var got account
	_, err := run(t, "GET", "/u/1?"+valid(), "", "",
		func(c *zarp.Context) error { return bindAndValidateQuery(c, &got) })
	if err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
}

func TestValidateRules(t *testing.T) {
	// The field names are the ones the client used — the form tags — not the Go
	// identifiers behind them.
	tests := []struct{ name, query, wantField string }{
		{"required", "email=o@example.com&role=admin&age=30&code=abcd", "name"},
		{"min length", "name=o&email=o@example.com&role=admin&age=30&code=abcd", "name"},
		{"max length", "name=octocatlong&email=o@example.com&role=admin&age=30&code=abcd", "name"},
		{"email shape", "name=octo&email=nope&role=admin&age=30&code=abcd", "email"},
		{"oneof", "name=octo&email=o@example.com&role=root&age=30&code=abcd", "role"},
		{"min value", "name=octo&email=o@example.com&role=admin&age=9&code=abcd", "age"},
		{"exact len", "name=octo&email=o@example.com&role=admin&age=30&code=ab", "code"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got account
			_, err := run(t, "GET", "/u/1?"+tc.query, "", "",
				func(c *zarp.Context) error { return bindAndValidateQuery(c, &got) })

			if err == nil {
				t.Fatalf("want a validation error naming %s", tc.wantField)
			}
			if !strings.Contains(err.Error(), tc.wantField) {
				t.Errorf("err = %v, want it to name %s", err, tc.wantField)
			}
		})
	}
}

func TestValidateReportsEveryFailure(t *testing.T) {
	var got account
	_, err := run(t, "GET", "/u/1?name=o&email=nope&role=root&age=1&code=x", "", "",
		func(c *zarp.Context) error { return bindAndValidateQuery(c, &got) })

	var errs binding.ValidationErrors
	if !errors.As(err, &errs) {
		t.Fatalf("err = %T, want ValidationErrors", err)
	}
	if len(errs) != 5 {
		t.Errorf("got %d failures (%v), want all 5 reported at once", len(errs), errs)
	}
}

func TestValidateOptionalFieldsSkipRules(t *testing.T) {
	// Age has min=18 but no required, so leaving it out is fine.
	type optional struct {
		Age int `form:"age" binding:"min=18"`
	}
	var got optional
	_, err := run(t, "GET", "/u/1", "", "",
		func(c *zarp.Context) error { return bindAndValidateQuery(c, &got) })
	if err != nil {
		t.Errorf("absent optional field failed its bound: %v", err)
	}
}

func TestValidateNestedStruct(t *testing.T) {
	type inner struct {
		Code string `form:"code" binding:"required"`
	}
	type outer struct {
		Name  string `form:"name" binding:"required"`
		Inner inner
	}
	var got outer
	_, err := run(t, "GET", "/u/1?name=x", "", "",
		func(c *zarp.Context) error { return bindAndValidateQuery(c, &got) })

	if err == nil || !strings.Contains(err.Error(), "code") {
		t.Errorf("err = %v, want the nested field reported", err)
	}
}

func TestValidateUnknownRule(t *testing.T) {
	type bad struct {
		X string `form:"x" binding:"definitelynotarule"`
	}
	var got bad
	_, err := run(t, "GET", "/u/1?x=1", "", "",
		func(c *zarp.Context) error { return bindAndValidateQuery(c, &got) })

	if err == nil || !strings.Contains(err.Error(), "unknown rule") {
		t.Errorf("err = %v, want an unknown-rule complaint", err)
	}
}

func TestEmailShapes(t *testing.T) {
	type e struct {
		Addr string `form:"addr" binding:"email"`
	}
	ok := []string{"a@b.co", "first.last+tag@sub.example.com"}
	bad := []string{"nope", "@example.com", "a@", "a@b", "a b@example.com", "a@b@c.com"}

	// Escape the addresses: a bare "+" in a query string decodes to a space.
	for _, addr := range ok {
		var got e
		_, err := run(t, "GET", "/u/1?addr="+url.QueryEscape(addr), "", "",
			func(c *zarp.Context) error { return bindAndValidateQuery(c, &got) })
		if err != nil {
			t.Errorf("%q rejected: %v", addr, err)
		}
	}
	for _, addr := range bad {
		var got e
		_, err := run(t, "GET", "/u/1?addr="+url.QueryEscape(addr), "", "",
			func(c *zarp.Context) error { return bindAndValidateQuery(c, &got) })
		if err == nil {
			t.Errorf("%q accepted", addr)
		}
	}
}

// ---------------------------------------------------------------- benchmarks

func BenchmarkBindQuery(b *testing.B) {
	e := zarp.New()
	e.GET("/s", func(c *zarp.Context) {
		var s search
		binding.Query(c, &s)
	})
	req := httptest.NewRequest(http.MethodGet, "/s?q=go&page=2&ratio=1.5&live=true", nil)
	rec := httptest.NewRecorder()
	b.ReportAllocs()
	for b.Loop() {
		e.ServeHTTP(rec, req)
	}
}

func BenchmarkValidate(b *testing.B) {
	a := account{Name: "octo", Email: "o@example.com", Role: "admin", Age: 30, Code: "abcd"}
	b.ReportAllocs()
	for b.Loop() {
		if err := binding.Validate(&a); err != nil {
			b.Fatal(err)
		}
	}
}

// ---------------------------------------------------------------- one body, one document

func TestJSONRejectsTrailingContent(t *testing.T) {
	bodies := map[string]string{
		"second object": `{"name":"alice"} {"name":"mallory"}`,
		"second array":  `{"name":"alice"} [1,2]`,
		"garbage":       `{"name":"alice"} not-json`,
		"repeated":      `{"name":"alice"}{"name":"mallory"}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			var got user
			_, err := run(t, "POST", "/u/1", body, binding.MIMEJSON,
				func(c *zarp.Context) error { return binding.JSON(c, &got) })

			if !errors.Is(err, binding.ErrTrailingContent) {
				t.Errorf("err = %v, want ErrTrailingContent", err)
			}
		})
	}
}

func TestJSONAcceptsSurroundingWhitespace(t *testing.T) {
	var got user
	_, err := run(t, "POST", "/u/1", "  {\"name\":\"alice\"}\n\n", binding.MIMEJSON,
		func(c *zarp.Context) error { return binding.JSON(c, &got) })

	if err != nil {
		t.Fatalf("whitespace around a single value must bind: %v", err)
	}
	if got.Name != "alice" {
		t.Errorf("name = %q", got.Name)
	}
}

// ---------------------------------------------------------------- media types

func TestUnsupportedMediaType(t *testing.T) {
	var got user
	_, err := run(t, "POST", "/u/1", "<user/>", "application/xml",
		func(c *zarp.Context) error { return binding.Bind(c, &got) })

	if !errors.Is(err, binding.ErrUnsupportedMediaType) {
		t.Fatalf("err = %v, want ErrUnsupportedMediaType", err)
	}
	var typed *binding.UnsupportedMediaTypeError
	if !errors.As(err, &typed) {
		t.Fatalf("err = %v, want an *UnsupportedMediaTypeError", err)
	}
	if typed.ContentType != "application/xml" {
		t.Errorf("ContentType = %q", typed.ContentType)
	}
	// The message has to name the type, or a 415 tells the client nothing.
	if !strings.Contains(err.Error(), "application/xml") {
		t.Errorf("err = %v, want it to name the media type", err)
	}
}

func TestUnsupportedMediaTypeWithoutContentType(t *testing.T) {
	var got user
	_, err := run(t, "POST", "/u/1", "whatever", "",
		func(c *zarp.Context) error { return binding.Bind(c, &got) })

	if !errors.Is(err, binding.ErrUnsupportedMediaType) {
		t.Errorf("err = %v, want ErrUnsupportedMediaType", err)
	}
}

// ---------------------------------------------------------------- bind and validate

func TestBindDoesNotValidate(t *testing.T) {
	// name is below its min and role is not in the oneof set, yet binding alone
	// must succeed: validation is the caller's separate, explicit step.
	var got account
	_, err := run(t, "GET", "/u/1?name=x&role=root", "", "",
		func(c *zarp.Context) error { return binding.Query(c, &got) })

	if err != nil {
		t.Fatalf("bind reported a validation failure: %v", err)
	}
	if got.Name != "x" {
		t.Errorf("name = %q, want the value to have been bound anyway", got.Name)
	}
	if err := binding.Validate(got); err == nil {
		t.Error("Validate accepted the same value the rules forbid")
	}
}

func TestBindAndValidateRunsBoth(t *testing.T) {
	var got account
	_, err := run(t, "GET", "/u/1?name=x&role=root", "", "",
		func(c *zarp.Context) error { return binding.BindAndValidate(c, &got) })

	var invalid binding.ValidationErrors
	if !errors.As(err, &invalid) {
		t.Fatalf("err = %v, want ValidationErrors", err)
	}
	if len(invalid) == 0 {
		t.Error("no field errors reported")
	}
}

// ---------------------------------------------------------------- numeric bounds

func TestBoundsBeyondFloat64Precision(t *testing.T) {
	// The bound is 2^53, the last integer before float64 starts skipping them.
	// 2^53+1 rounds to 2^53 as a float64, so comparing through float64 finds the
	// two equal and lets an over-bound value through; comparing as int64 does
	// not. This is the case that separates the two implementations.
	type big struct {
		N int64 `form:"n" binding:"max=9007199254740992"`
	}

	atLimit := big{N: 9007199254740992}
	if err := binding.Validate(atLimit); err != nil {
		t.Errorf("value equal to the bound rejected: %v", err)
	}

	over := big{N: 9007199254740993}
	if err := binding.Validate(over); err == nil {
		t.Error("value above the bound accepted: it was rounded onto the bound")
	}
}

func TestBoundsOnUnsignedFields(t *testing.T) {
	type counts struct {
		Big      uint64 `form:"big"  binding:"min=18446744073709551615"`
		Negative uint8  `form:"neg"  binding:"min=-1"`
	}

	// The largest uint64 there is, as its own minimum.
	ok := counts{Big: 18446744073709551615, Negative: 0}
	if err := binding.Validate(ok); err != nil {
		t.Errorf("uint64 max rejected by its own bound: %v", err)
	}

	low := counts{Big: 1, Negative: 0}
	if err := binding.Validate(low); err == nil {
		t.Error("value below a uint64 bound accepted")
	}
}

func TestFieldErrorNamesFollowTheRequest(t *testing.T) {
	type req struct {
		JSONName string `json:"json_name" binding:"required"`
		FormName string `form:"form_name" binding:"required"`
		URIName  string `uri:"uri_name"   binding:"required"`
		Untagged string `binding:"required"`
	}

	err := binding.Validate(req{})
	var invalid binding.ValidationErrors
	if !errors.As(err, &invalid) {
		t.Fatalf("err = %v, want ValidationErrors", err)
	}

	got := make(map[string]bool, len(invalid))
	for _, fe := range invalid {
		got[fe.Field] = true
	}
	for _, want := range []string{"json_name", "form_name", "uri_name", "Untagged"} {
		if !got[want] {
			t.Errorf("no error named %q; got %v", want, invalid)
		}
	}
}

func TestJSONTrailingContentEdgeCases(t *testing.T) {
	// Decoder.More reports false on the tokens that close an array or object,
	// so these two are exactly the bodies it let through.
	rejected := map[string]string{
		"closing brace":  `{"name":"a"} }`,
		"closing square": `{"name":"a"} ]`,
		"comma":          `{"name":"a"} ,`,
		"number":         `{"name":"a"} 5`,
		"string":         `{"name":"a"} "x"`,
		"null":           `{"name":"a"} null`,
		"true":           `{"name":"a"} true`,
	}
	for name, body := range rejected {
		t.Run(name, func(t *testing.T) {
			var got user
			_, err := run(t, "POST", "/u/1", body, binding.MIMEJSON,
				func(c *zarp.Context) error { return binding.JSON(c, &got) })

			if !errors.Is(err, binding.ErrTrailingContent) {
				t.Errorf("body %q: err = %v, want ErrTrailingContent", body, err)
			}
		})
	}

	accepted := map[string]string{
		"bare":               `{"name":"a"}`,
		"trailing newline":   "{\"name\":\"a\"}\n",
		"trailing space":     `{"name":"a"}   `,
		"surrounding spaces": "  {\"name\":\"a\"} \n\t ",
	}
	for name, body := range accepted {
		t.Run(name, func(t *testing.T) {
			var got user
			_, err := run(t, "POST", "/u/1", body, binding.MIMEJSON,
				func(c *zarp.Context) error { return binding.JSON(c, &got) })

			if err != nil {
				t.Errorf("body %q rejected: %v", body, err)
			}
		})
	}
}

func TestFormBindingReportsAParseFailure(t *testing.T) {
	// A body that is not parseable as a form must be an error, not a struct
	// full of zero values that looks like a client who sent nothing.
	type form struct {
		Name string `form:"name"`
	}
	var got form
	var err error

	e := zarp.New()
	e.POST("/x", func(c *zarp.Context) { err = binding.Form(c, &got) })

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("name=a;b=%zz"))
	r.Header.Set("Content-Type", binding.MIMEPOSTForm)
	e.ServeHTTP(rec, r)

	if err == nil {
		t.Fatalf("malformed form body bound cleanly as %+v", got)
	}
}

func TestDefaultMatchesMediaTypeCaseInsensitively(t *testing.T) {
	// RFC 9110 §8.3.1: a media type's type and subtype are case-insensitive,
	// so a client may spell them however it likes.
	tests := []struct{ contentType, want string }{
		{"application/json", "json"},
		{"Application/JSON", "json"},
		{"APPLICATION/JSON", "json"},
		{"APPLICATION/JSON; charset=utf-8", "json"},
		{"Application/Json ; charset=utf-8", "json"},
		{"multipart/form-data; boundary=x", "multipart"},
		{"Multipart/Form-Data; boundary=x", "multipart"},
		{"MULTIPART/FORM-DATA; boundary=x", "multipart"},
		{"application/x-www-form-urlencoded", "form"},
		{"Application/X-WWW-Form-Urlencoded", "form"},
		// Still unsupported, whatever the casing.
		{"application/xml", "unsupported"},
		{"Application/XML", "unsupported"},
	}
	for _, tc := range tests {
		t.Run(tc.contentType, func(t *testing.T) {
			if got := binding.Default(http.MethodPost, tc.contentType).Name(); got != tc.want {
				t.Errorf("Default(POST, %q) = %s, want %s", tc.contentType, got, tc.want)
			}
		})
	}
}

func TestBindAcceptsAMixedCaseJSONContentType(t *testing.T) {
	var got user
	_, err := run(t, "POST", "/u/1", `{"name":"octocat"}`, "Application/JSON; charset=utf-8",
		func(c *zarp.Context) error { return binding.Bind(c, &got) })

	if err != nil {
		t.Fatalf("mixed-case Content-Type rejected: %v", err)
	}
	if got.Name != "octocat" {
		t.Errorf("name = %q", got.Name)
	}
}

func TestBinderAccessorsAreStableAndUsable(t *testing.T) {
	// The binders are reached through functions rather than exported variables,
	// so an importer cannot swap one out for the whole process. What the
	// functions must still guarantee is that they hand back the same binder
	// every time — a fresh one per call would allocate on every bind.
	accessors := map[string]func() binding.Binding{
		"json":      binding.JSONBinding,
		"query":     binding.QueryBinding,
		"form":      binding.FormBinding,
		"multipart": binding.MultipartBinding,
		"uri":       binding.URIBinding,
		"header":    binding.HeaderBinding,
	}
	for name, accessor := range accessors {
		t.Run(name, func(t *testing.T) {
			b := accessor()
			if b == nil {
				t.Fatal("accessor returned nil")
			}
			if b.Name() != name {
				t.Errorf("Name() = %q, want %q", b.Name(), name)
			}
			if b != accessor() {
				t.Error("two calls returned different binders; each bind would allocate one")
			}
		})
	}
}

func TestJSONBindingCarriesTheDefaultConfig(t *testing.T) {
	// The default binder must be the safe one: a 4 MB cap, not an unbounded
	// decode.
	var got user
	body := `{"name":"` + strings.Repeat("x", int(binding.DefaultMaxBodyBytes)+1) + `"}`

	_, err := run(t, "POST", "/u/1", body, binding.MIMEJSON,
		func(c *zarp.Context) error { return binding.With(c, &got, binding.JSONBinding()) })

	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("err = %v, want the default body cap to reject an oversized body", err)
	}
}
