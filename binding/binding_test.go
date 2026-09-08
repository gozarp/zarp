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

	"github.com/subhanjanops/gomicro"
	"github.com/subhanjanops/gomicro/binding"
)

// run registers a handler that binds, and returns whatever it recorded.
func run(t *testing.T, method, target, body, contentType string, bind func(*gomicro.Context) error) (error, *httptest.ResponseRecorder) {
	t.Helper()
	var bindErr error

	e := gomicro.New()
	e.Handle(method, "/u/:id", func(c *gomicro.Context) {
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
	return bindErr, rec
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
	err, _ := run(t, "POST", "/u/1", `{"name":"octocat","age":7,"tags":["a","b"],"admin":true}`,
		binding.MIMEJSON, func(c *gomicro.Context) error { return binding.JSON(c, &got) })

	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if got.Name != "octocat" || got.Age != 7 || len(got.Tags) != 2 || !got.Admin {
		t.Errorf("got %+v", got)
	}
}

func TestJSONEmptyBody(t *testing.T) {
	var got user
	err, _ := run(t, "POST", "/u/1", "", binding.MIMEJSON,
		func(c *gomicro.Context) error { return binding.JSON(c, &got) })

	if !errors.Is(err, binding.ErrEmptyBody) {
		t.Errorf("err = %v, want ErrEmptyBody", err)
	}
}

func TestJSONMalformed(t *testing.T) {
	var got user
	err, _ := run(t, "POST", "/u/1", `{"name":`, binding.MIMEJSON,
		func(c *gomicro.Context) error { return binding.JSON(c, &got) })

	if err == nil {
		t.Fatal("want an error")
	}
	if errors.Is(err, binding.ErrEmptyBody) {
		t.Error("a truncated body is not an empty one")
	}
}

func TestJSONBodyCap(t *testing.T) {
	old := binding.MaxBodyBytes
	binding.MaxBodyBytes = 32
	defer func() { binding.MaxBodyBytes = old }()

	var got user
	err, _ := run(t, "POST", "/u/1", `{"name":"`+strings.Repeat("x", 200)+`"}`, binding.MIMEJSON,
		func(c *gomicro.Context) error { return binding.JSON(c, &got) })

	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("err = %v, want a body-size error", err)
	}
}

func TestJSONDisallowUnknownFields(t *testing.T) {
	binding.EnableDecoderDisallowUnknownFields = true
	defer func() { binding.EnableDecoderDisallowUnknownFields = false }()

	var got user
	err, _ := run(t, "POST", "/u/1", `{"name":"x","nope":1}`, binding.MIMEJSON,
		func(c *gomicro.Context) error { return binding.JSON(c, &got) })

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
	err, _ := run(t, "GET", target, "", "",
		func(c *gomicro.Context) error { return binding.Query(c, &got) })

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
	err, _ := run(t, "GET", "/u/1?-=nope&Skipped=nope", "", "",
		func(c *gomicro.Context) error { return binding.Query(c, &got) })
	if err != nil {
		t.Fatal(err)
	}
	if got.Skipped != "" {
		t.Errorf(`field tagged "-" was bound: %q`, got.Skipped)
	}
}

func TestQueryBadValue(t *testing.T) {
	var got search
	err, _ := run(t, "GET", "/u/1?page=abc", "", "",
		func(c *gomicro.Context) error { return binding.Query(c, &got) })

	if err == nil || !strings.Contains(err.Error(), "Page") {
		t.Errorf("err = %v, want it to name the field", err)
	}
}

func TestForm(t *testing.T) {
	var got search
	err, _ := run(t, "POST", "/u/1", "q=go&page=3", binding.MIMEPOSTForm,
		func(c *gomicro.Context) error { return binding.Form(c, &got) })

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
	e := gomicro.New()
	e.POST("/upload", func(c *gomicro.Context) {
		if err := binding.Multipart(c, &got); err != nil {
			t.Errorf("bind: %v", err)
		}
		if fh, err := c.FormFile("upload"); err == nil {
			fileName = fh.Filename
		}
	})

	r := httptest.NewRequest("POST", "/upload", &body)
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

	e := gomicro.New()
	e.GET("/u/:id/:slug", func(c *gomicro.Context) {
		if err := binding.URI(c, &got); err != nil {
			t.Errorf("bind: %v", err)
		}
	})
	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/u/42/hello", nil))

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

	e := gomicro.New()
	e.GET("/h", func(c *gomicro.Context) {
		if err := binding.Header(c, &got); err != nil {
			t.Errorf("bind: %v", err)
		}
	})
	r := httptest.NewRequest("GET", "/h", nil)
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
		{"POST", "text/plain", "json"},
	}
	for _, tc := range tests {
		if got := binding.Default(tc.method, tc.contentType).Name(); got != tc.want {
			t.Errorf("Default(%s, %q) = %s, want %s", tc.method, tc.contentType, got, tc.want)
		}
	}
}

func TestBindPicksByContentType(t *testing.T) {
	var got user
	err, _ := run(t, "POST", "/u/1", `{"name":"via-bind"}`, binding.MIMEJSON,
		func(c *gomicro.Context) error { return binding.Bind(c, &got) })

	if err != nil || got.Name != "via-bind" {
		t.Errorf("err=%v got=%+v", err, got)
	}
}

func TestBindRejectsNonPointer(t *testing.T) {
	var got search
	err, _ := run(t, "GET", "/u/1?q=x", "", "",
		func(c *gomicro.Context) error { return binding.Query(c, got) })

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

func valid() string {
	return "name=octo&email=o@example.com&role=admin&age=30&code=abcd"
}

func TestValidateAccepts(t *testing.T) {
	var got account
	err, _ := run(t, "GET", "/u/1?"+valid(), "", "",
		func(c *gomicro.Context) error { return binding.Query(c, &got) })
	if err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
}

func TestValidateRules(t *testing.T) {
	tests := []struct{ name, query, wantField string }{
		{"required", "email=o@example.com&role=admin&age=30&code=abcd", "Name"},
		{"min length", "name=o&email=o@example.com&role=admin&age=30&code=abcd", "Name"},
		{"max length", "name=octocatlong&email=o@example.com&role=admin&age=30&code=abcd", "Name"},
		{"email shape", "name=octo&email=nope&role=admin&age=30&code=abcd", "Email"},
		{"oneof", "name=octo&email=o@example.com&role=root&age=30&code=abcd", "Role"},
		{"min value", "name=octo&email=o@example.com&role=admin&age=9&code=abcd", "Age"},
		{"exact len", "name=octo&email=o@example.com&role=admin&age=30&code=ab", "Code"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got account
			err, _ := run(t, "GET", "/u/1?"+tc.query, "", "",
				func(c *gomicro.Context) error { return binding.Query(c, &got) })

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
	err, _ := run(t, "GET", "/u/1?name=o&email=nope&role=root&age=1&code=x", "", "",
		func(c *gomicro.Context) error { return binding.Query(c, &got) })

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
	err, _ := run(t, "GET", "/u/1", "", "",
		func(c *gomicro.Context) error { return binding.Query(c, &got) })
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
	err, _ := run(t, "GET", "/u/1?name=x", "", "",
		func(c *gomicro.Context) error { return binding.Query(c, &got) })

	if err == nil || !strings.Contains(err.Error(), "Code") {
		t.Errorf("err = %v, want the nested field reported", err)
	}
}

func TestValidateUnknownRule(t *testing.T) {
	type bad struct {
		X string `form:"x" binding:"definitelynotarule"`
	}
	var got bad
	err, _ := run(t, "GET", "/u/1?x=1", "", "",
		func(c *gomicro.Context) error { return binding.Query(c, &got) })

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
		err, _ := run(t, "GET", "/u/1?addr="+url.QueryEscape(addr), "", "",
			func(c *gomicro.Context) error { return binding.Query(c, &got) })
		if err != nil {
			t.Errorf("%q rejected: %v", addr, err)
		}
	}
	for _, addr := range bad {
		var got e
		err, _ := run(t, "GET", "/u/1?addr="+url.QueryEscape(addr), "", "",
			func(c *gomicro.Context) error { return binding.Query(c, &got) })
		if err == nil {
			t.Errorf("%q accepted", addr)
		}
	}
}

// ---------------------------------------------------------------- benchmarks

func BenchmarkBindQuery(b *testing.B) {
	e := gomicro.New()
	e.GET("/s", func(c *gomicro.Context) {
		var s search
		binding.Query(c, &s)
	})
	req := httptest.NewRequest("GET", "/s?q=go&page=2&ratio=1.5&live=true", nil)
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
