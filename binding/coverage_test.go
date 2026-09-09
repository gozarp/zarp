package binding_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gozarp/zarp"
	"github.com/gozarp/zarp/binding"
)

// bindQuery runs a query-string bind against target and returns the error.
func bindQuery(t *testing.T, query string, target any) error {
	t.Helper()
	var err error
	e := zarp.New()
	e.GET("/x", func(c *zarp.Context) { err = binding.Query(c, target) })
	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x?"+query, nil))
	return err
}

func TestBindingNames(t *testing.T) {
	want := map[binding.Binding]string{
		binding.JSONBinding:      "json",
		binding.QueryBinding:     "query",
		binding.FormBinding:      "form",
		binding.MultipartBinding: "multipart",
		binding.URIBinding:       "uri",
		binding.HeaderBinding:    "header",
	}
	for b, name := range want {
		if got := b.Name(); got != name {
			t.Errorf("Name() = %q, want %q", got, name)
		}
	}
}

// ---------------------------------------------------------------- scalars

func TestBindEveryScalarWidth(t *testing.T) {
	var got struct {
		I8   int8    `form:"i8"`
		I16  int16   `form:"i16"`
		I32  int32   `form:"i32"`
		I64  int64   `form:"i64"`
		U    uint    `form:"u"`
		U8   uint8   `form:"u8"`
		U16  uint16  `form:"u16"`
		U32  uint32  `form:"u32"`
		U64  uint64  `form:"u64"`
		F32  float32 `form:"f32"`
		Flag bool    `form:"flag"`
	}
	err := bindQuery(t, "i8=1&i16=2&i32=3&i64=4&u=5&u8=6&u16=7&u32=8&u64=9&f32=1.5&flag=1", &got)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if got.I8 != 1 || got.I16 != 2 || got.I32 != 3 || got.I64 != 4 {
		t.Errorf("signed: %+v", got)
	}
	if got.U != 5 || got.U8 != 6 || got.U16 != 7 || got.U32 != 8 || got.U64 != 9 {
		t.Errorf("unsigned: %+v", got)
	}
	if got.F32 != 1.5 || !got.Flag {
		t.Errorf("float/bool: %+v", got)
	}
}

func TestBindScalarErrors(t *testing.T) {
	tests := []struct {
		name, query string
		target      func() any
		want        string
	}{
		{"bad bool", "b=maybe", func() any {
			return &struct {
				B bool `form:"b"`
			}{}
		}, "bool"},
		{"bad uint", "u=-1", func() any {
			return &struct {
				U uint `form:"u"`
			}{}
		}, "unsigned"},
		{"bad float", "f=xyz", func() any {
			return &struct {
				F float64 `form:"f"`
			}{}
		}, "number"},
		{"bad duration", "d=soon", func() any {
			return &struct {
				D time.Duration `form:"d"`
			}{}
		}, "duration"},
		{"bad time", "t=yesterday", func() any {
			return &struct {
				T time.Time `form:"t"`
			}{}
		}, "RFC 3339"},
		{"unsupported kind", "c=1", func() any {
			return &struct {
				C complex128 `form:"c"`
			}{}
		}, "unsupported"},
		{"bad element in slice", "n=1&n=two", func() any {
			return &struct {
				N []int `form:"n"`
			}{}
		}, "integer"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := bindQuery(t, tc.query, tc.target())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestBindTimeAndPointerScalar(t *testing.T) {
	var got struct {
		When time.Time      `form:"when"`
		Rate *float64       `form:"rate"`
		TTL  *time.Duration `form:"ttl"`
	}
	err := bindQuery(t, "when=2026-01-02T15:04:05Z&rate=2.5&ttl=90s", &got)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if got.When.Year() != 2026 || got.When.Minute() != 4 {
		t.Errorf("time = %v", got.When)
	}
	if got.Rate == nil || *got.Rate != 2.5 {
		t.Errorf("rate = %v", got.Rate)
	}
	if got.TTL == nil || *got.TTL != 90*time.Second {
		t.Errorf("ttl = %v", got.TTL)
	}
}

func TestBindNestedAndEmbeddedStructs(t *testing.T) {
	// The embedded type must be exported: embedding an unexported one makes the
	// field itself unexported, and reflect cannot set it.
	type Page struct {
		Limit  int `form:"limit"`
		Offset int `form:"offset"`
	}
	var got struct {
		Q      string `form:"q"`
		Page          // embedded, flattened
		Nested Page
	}
	if err := bindQuery(t, "q=go&limit=10&offset=20", &got); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if got.Q != "go" || got.Limit != 10 || got.Offset != 20 {
		t.Errorf("embedded not flattened: %+v", got)
	}
	if got.Nested.Limit != 10 {
		t.Errorf("nested struct not filled: %+v", got.Nested)
	}
}

func TestBindSkipsUnexportedFields(t *testing.T) {
	type page struct {
		Limit int `form:"limit"`
	}
	var got struct {
		Q    string `form:"q"`
		page        // unexported embedded field: unsettable, must be skipped
	}
	if err := bindQuery(t, "q=go&limit=10", &got); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if got.Q != "go" {
		t.Errorf("Q = %q", got.Q)
	}
	if got.Limit != 0 {
		t.Errorf("unexported embedded field was written: %+v", got.page)
	}
}

func TestBindTargetMustPointToStruct(t *testing.T) {
	n := 0
	err := bindQuery(t, "x=1", &n)
	if err == nil || !strings.Contains(err.Error(), "struct") {
		t.Errorf("err = %v, want a struct complaint", err)
	}
}

// ---------------------------------------------------------------- json

func TestJSONUseNumber(t *testing.T) {
	numbers := binding.JSONWith(binding.JSONConfig{UseNumber: true})

	var got struct {
		N any `json:"n"`
	}
	var err error
	e := zarp.New()
	e.POST("/x", func(c *zarp.Context) { err = binding.With(c, &got, numbers) })

	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"n":10000000000000000001}`))
	req.Header.Set("Content-Type", binding.MIMEJSON)
	e.ServeHTTP(httptest.NewRecorder(), req)

	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	// Without UseNumber this would come back as a lossy float64.
	if s, ok := got.N.(interface{ String() string }); !ok || s.String() != "10000000000000000001" {
		t.Errorf("n = %#v, want an exact json.Number", got.N)
	}
}

func TestJSONNilBody(t *testing.T) {
	c := &zarp.Context{}
	var target struct{}
	if err := binding.JSONBinding.Bind(c, &target); !errors.Is(err, binding.ErrEmptyBody) {
		t.Errorf("err = %v, want ErrEmptyBody for a nil request", err)
	}
}

func TestMultipartOnNonMultipartBody(t *testing.T) {
	var err error
	e := zarp.New()
	e.POST("/x", func(c *zarp.Context) {
		var target struct{}
		err = binding.Multipart(c, &target)
	})
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("a=b"))
	req.Header.Set("Content-Type", binding.MIMEPOSTForm)
	e.ServeHTTP(httptest.NewRecorder(), req)

	if err == nil {
		t.Error("want an error binding multipart from a urlencoded body")
	}
}

// ---------------------------------------------------------------- validator

func TestValidateIgnoresNonStructs(t *testing.T) {
	if err := binding.Validate(nil); err != nil {
		t.Errorf("Validate(nil) = %v", err)
	}
	var nilPtr *struct{ X string }
	if err := binding.Validate(nilPtr); err != nil {
		t.Errorf("Validate(nil pointer) = %v", err)
	}
	n := 42
	if err := binding.Validate(&n); err != nil {
		t.Errorf("Validate(*int) = %v", err)
	}
}

func TestValidateMalformedRuleParams(t *testing.T) {
	type bad struct {
		Min string `binding:"min=abc"`
		Max string `binding:"max=abc"`
		Len string `binding:"len=abc"`
	}
	err := binding.Validate(&bad{Min: "v", Max: "v", Len: "v"})
	if err == nil || !strings.Contains(err.Error(), "invalid rule") {
		t.Errorf("err = %v, want an invalid-rule complaint", err)
	}
}

func TestValidateEmailOnNonString(t *testing.T) {
	type bad struct {
		N int `binding:"email"`
	}
	err := binding.Validate(&bad{N: 5})
	if err == nil || !strings.Contains(err.Error(), "applies to strings") {
		t.Errorf("err = %v", err)
	}
}

func TestValidateBoundsOnEveryNumericKind(t *testing.T) {
	type nums struct {
		U   uint    `binding:"min=2"`
		F   float64 `binding:"max=1.5"`
		S   []int   `binding:"min=2"`
		Ptr *int    `binding:"min=10"`
	}
	small := 1
	err := binding.Validate(&nums{U: 1, F: 9, S: []int{1}, Ptr: &small})

	var errs binding.ValidationErrors
	if !errors.As(err, &errs) {
		t.Fatalf("err = %v, want ValidationErrors", err)
	}
	if len(errs) != 4 {
		t.Errorf("got %d failures (%v), want one per field", len(errs), errs)
	}
}

func TestValidateSkipsDashAndUntagged(t *testing.T) {
	type skipped struct {
		Ignored  string `binding:"-"`
		Untagged string
		Checked  string `binding:"required"`
	}
	if err := binding.Validate(&skipped{Checked: "ok"}); err != nil {
		t.Errorf("err = %v, want the untagged and skipped fields ignored", err)
	}
}

func TestValidateNestedPointerStruct(t *testing.T) {
	type inner struct {
		Code string `binding:"required"`
	}
	type outer struct {
		Inner *inner
	}
	if err := binding.Validate(&outer{Inner: &inner{}}); err == nil {
		t.Error("want the failure inside a pointer-to-struct field")
	}
	if err := binding.Validate(&outer{}); err != nil {
		t.Errorf("a nil nested pointer should be skipped, got %v", err)
	}
}

func TestFieldErrorMessage(t *testing.T) {
	e := binding.FieldError{Field: "Name", Rule: "required", Msg: "is required"}
	if got := e.Error(); got != "Name: is required" {
		t.Errorf("Error() = %q", got)
	}
}

// ---------------------------------------------------------------- header/uri

func TestHeaderBindingWithoutRequest(t *testing.T) {
	c := &zarp.Context{}
	var target struct {
		X string `header:"X-Thing"`
	}
	if err := binding.HeaderBinding.Bind(c, &target); err != nil {
		t.Errorf("err = %v, want a nil request to bind nothing", err)
	}
	if target.X != "" {
		t.Errorf("X = %q", target.X)
	}
}

func TestURIBindingSkipsAbsentParams(t *testing.T) {
	var got struct {
		ID      int    `uri:"id"`
		Missing string `uri:"missing"`
	}
	var err error
	e := zarp.New()
	e.GET("/u/:id", func(c *zarp.Context) { err = binding.URI(c, &got) })
	e.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/u/7", nil))

	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	if got.ID != 7 || got.Missing != "" {
		t.Errorf("got %+v", got)
	}
}

func TestDefaultBindingOnHeadAndOptions(t *testing.T) {
	if got := binding.Default(http.MethodHead, "").Name(); got != "query" {
		t.Errorf("HEAD -> %s", got)
	}
	if got := binding.Default(http.MethodOptions, "").Name(); got != "query" {
		t.Errorf("OPTIONS -> %s", got)
	}
}

func TestBoundsOnFloatAndOtherKinds(t *testing.T) {
	type sample struct {
		Ratio float64 `form:"ratio" binding:"min=0.5,max=1.5"`
	}

	if err := binding.Validate(sample{Ratio: 1.0}); err != nil {
		t.Errorf("in-range float rejected: %v", err)
	}
	if err := binding.Validate(sample{Ratio: 0.25}); err == nil {
		t.Error("float below min accepted")
	}
	if err := binding.Validate(sample{Ratio: 2.5}); err == nil {
		t.Error("float above max accepted")
	}
}

func TestBoundsOnAnUnmeasurableKind(t *testing.T) {
	// A bound on a bool is a mistake in the tag, and the message should say so
	// rather than inventing a zero to compare against.
	type odd struct {
		Enabled bool `form:"enabled" binding:"min=1"`
	}

	err := binding.Validate(odd{Enabled: true})
	if err == nil {
		t.Fatal("a bound on a bool passed silently")
	}
	if !strings.Contains(err.Error(), "does not apply to bool") {
		t.Errorf("err = %v, want it to say the rule does not apply", err)
	}
}

func TestBoundsWithUnparseableParam(t *testing.T) {
	type broken struct {
		S string  `form:"s" binding:"min=abc"`
		I int     `form:"i" binding:"max=abc"`
		U uint    `form:"u" binding:"min=abc"`
		F float64 `form:"f" binding:"len=abc"`
	}

	err := binding.Validate(broken{S: "x", I: 1, U: 1, F: 1})
	var invalid binding.ValidationErrors
	if !errors.As(err, &invalid) {
		t.Fatalf("err = %v, want ValidationErrors", err)
	}
	if len(invalid) != 4 {
		t.Errorf("got %d errors, want one per field: %v", len(invalid), invalid)
	}
	for _, fe := range invalid {
		if !strings.Contains(fe.Msg, "invalid rule") {
			t.Errorf("%s: msg = %q, want it to name the bad rule", fe.Field, fe.Msg)
		}
	}
}

func TestUnsupportedMediaTypeMessages(t *testing.T) {
	withType := &binding.UnsupportedMediaTypeError{ContentType: "application/xml"}
	if !strings.Contains(withType.Error(), `"application/xml"`) {
		t.Errorf("Error = %q, want it to quote the type", withType.Error())
	}

	none := &binding.UnsupportedMediaTypeError{}
	if !strings.Contains(none.Error(), "no Content-Type") {
		t.Errorf("Error = %q, want it to say the header was missing", none.Error())
	}
	if !errors.Is(none, binding.ErrUnsupportedMediaType) {
		t.Error("an empty-type error must still match ErrUnsupportedMediaType")
	}
}

func TestBindAndValidateStopsAtBindFailure(t *testing.T) {
	type req struct {
		Name string `json:"name" binding:"required"`
	}
	var got req
	var err error

	e := zarp.New()
	e.POST("/x", func(c *zarp.Context) { err = binding.BindAndValidate(c, &got) })

	body := strings.NewReader(`{"name":`)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/x", body)
	r.Header.Set("Content-Type", binding.MIMEJSON)
	e.ServeHTTP(rec, r)

	// A malformed body is a bind failure, not a validation failure: the caller
	// needs to tell 400 from 422.
	var invalid binding.ValidationErrors
	if err == nil || errors.As(err, &invalid) {
		t.Errorf("err = %v, want a decode error rather than ValidationErrors", err)
	}
}

func TestBindAndValidateAcceptsAGoodRequest(t *testing.T) {
	type req struct {
		Name string `json:"name" binding:"required,min=2"`
	}
	var got req
	var err error

	e := zarp.New()
	e.POST("/x", func(c *zarp.Context) { err = binding.BindAndValidate(c, &got) })

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"name":"octocat"}`))
	r.Header.Set("Content-Type", binding.MIMEJSON)
	e.ServeHTTP(rec, r)

	if err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	if got.Name != "octocat" {
		t.Errorf("name = %q", got.Name)
	}
}
