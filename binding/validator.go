package binding

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
)

// The rule set is deliberately closed: required, min, max, len, oneof and
// email. Every further rule is more reflection surface and more to maintain,
// and a third-party validator is ruled out by the module's no-dependency rule.
// Anything more specific belongs in the handler, where it can say what it means.
//
//	type CreateUser struct {
//		Name  string `json:"name"  binding:"required,min=2,max=64"`
//		Email string `json:"email" binding:"required,email"`
//		Role  string `json:"role"  binding:"oneof=admin member"`
//	}
//
// min and max bound a number's value and a string's or slice's length. len
// fixes that length exactly.

// FieldError is one field failing one rule.
type FieldError struct {
	// Field names the field as the request did: the `json`, `form`, `uri` or
	// `header` tag it was bound from, falling back to the Go field name only
	// when there is no tag. A client that sent "user_name" is told about
	// "user_name", not about a Go identifier it has never seen.
	Field string
	Rule  string // the rule that failed, e.g. "min=2"
	Msg   string
}

// Error implements error: the field name followed by what was wrong with it.
func (e FieldError) Error() string {
	return e.Field + ": " + e.Msg
}

// ValidationErrors is every failure found, not just the first: an API client
// fixing one field at a time is a bad experience.
type ValidationErrors []FieldError

// Error implements error, joining every failure so one response tells the
// client about all of them.
func (errs ValidationErrors) Error() string {
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = e.Error()
	}
	return "binding: " + strings.Join(parts, "; ")
}

// Validate checks obj against its `binding` tags. It is called for you by Bind
// and the shorthand functions; call it directly only for a value you built
// yourself.
func Validate(obj any) error {
	v := reflect.ValueOf(obj)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}

	var errs ValidationErrors
	validateStruct(v, &errs)
	if len(errs) == 0 {
		return nil
	}
	return errs
}

func validateStruct(v reflect.Value, errs *ValidationErrors) {
	for _, f := range fieldsOf(v.Type()) {
		value := v.Field(f.index)

		if f.nested {
			nested := value
			for nested.Kind() == reflect.Pointer && !nested.IsNil() {
				nested = nested.Elem()
			}
			if nested.Kind() == reflect.Struct {
				validateStruct(nested, errs)
			}
			continue
		}

		for _, r := range f.rules {
			if err := r.check(f.name, value); err != nil {
				*errs = append(*errs, *err)
			}
		}
	}
}

// ---------------------------------------------------------------- tag cache

type rule struct {
	name  string // "required", "min", ...
	param string // the part after '='
	raw   string // the whole clause, for the error message
}

type field struct {
	index  int
	name   string
	rules  []rule
	nested bool
}

// tagCache keeps the parsed tags per struct type. Parsing them on every request
// is the mistake this package exists to avoid making twice.
var tagCache sync.Map // reflect.Type -> []field

func fieldsOf(t reflect.Type) []field {
	if cached, ok := tagCache.Load(t); ok {
		return cached.([]field)
	}

	var fields []field
	for i := range t.NumField() {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}

		tag := sf.Tag.Get("binding")
		if tag == "-" {
			continue
		}

		ft := sf.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct && isPlainStruct(ft) && tag == "" {
			fields = append(fields, field{index: i, name: fieldName(sf), nested: true})
			continue
		}
		if tag == "" {
			continue
		}

		f := field{index: i, name: fieldName(sf)}
		for _, clause := range strings.Split(tag, ",") {
			clause = strings.TrimSpace(clause)
			if clause == "" {
				continue
			}
			name, param, _ := strings.Cut(clause, "=")
			f.rules = append(f.rules, rule{name: name, param: param, raw: clause})
		}
		fields = append(fields, f)
	}

	tagCache.Store(t, fields)
	return fields
}

// fieldName is what the client called this field. The binders read `json`,
// `form`, `uri` and `header` tags, so an error message should use the same
// name; the Go identifier is the last resort, not the first choice.
func fieldName(sf reflect.StructField) string {
	for _, key := range [...]string{"json", "form", "uri", "header"} {
		tag := sf.Tag.Get(key)
		if tag == "" || tag == "-" {
			continue
		}
		if name, _, _ := strings.Cut(tag, ","); name != "" {
			return name
		}
	}
	return sf.Name
}

// ---------------------------------------------------------------- rules

func (r rule) check(fieldName string, v reflect.Value) *FieldError {
	fail := func(format string, args ...any) *FieldError {
		return &FieldError{Field: fieldName, Rule: r.raw, Msg: fmt.Sprintf(format, args...)}
	}

	// Every rule but "required" passes on an absent value, so an optional field
	// with bounds is expressible: `binding:"min=2"` on an empty string is fine.
	if r.name != "required" && isZero(v) {
		return nil
	}

	switch r.name {
	case "required":
		if isZero(v) {
			return fail("is required")
		}

	case "min":
		cmp, kind, got, err := compare(v, r.param)
		if errors.Is(err, errUnmeasurable) {
			return fail("min does not apply to %s", deref(v).Kind())
		}
		if err != nil {
			return fail("invalid rule %q", r.raw)
		}
		if cmp < 0 {
			return fail("%s must be at least %s, got %s", kind, r.param, got)
		}

	case "max":
		cmp, kind, got, err := compare(v, r.param)
		if errors.Is(err, errUnmeasurable) {
			return fail("max does not apply to %s", deref(v).Kind())
		}
		if err != nil {
			return fail("invalid rule %q", r.raw)
		}
		if cmp > 0 {
			return fail("%s must be at most %s, got %s", kind, r.param, got)
		}

	case "len":
		cmp, kind, got, err := compare(v, r.param)
		if errors.Is(err, errUnmeasurable) {
			return fail("len does not apply to %s", deref(v).Kind())
		}
		if err != nil {
			return fail("invalid rule %q", r.raw)
		}
		if cmp != 0 {
			return fail("%s must be exactly %s, got %s", kind, r.param, got)
		}

	case "oneof":
		s := fmt.Sprint(deref(v).Interface())
		for _, allowed := range strings.Fields(r.param) {
			if s == allowed {
				return nil
			}
		}
		return fail("must be one of [%s], got %q", r.param, s)

	case "email":
		s, ok := deref(v).Interface().(string)
		if !ok {
			return fail("email applies to strings, not %s", v.Type())
		}
		if !looksLikeEmail(s) {
			return fail("%q is not a valid email address", s)
		}

	default:
		return fail("unknown rule %q", r.name)
	}
	return nil
}

// compare orders a field against the bound written in a rule, in whatever
// numeric domain the field itself uses: int64 for signed fields, uint64 for
// unsigned ones, float64 only for floats, and a count for strings, slices,
// arrays and maps.
//
// Doing this through float64 — one parse for every kind — is the obvious
// implementation and it is wrong: float64 holds integers exactly only up to
// 2^53, so `binding:"max=9007199254740993"` on an int64 field compares two
// values that have both been rounded to the same number, and the bound silently
// stops working. Cases like an ID or a nanosecond timestamp live well past that
// line.
//
// It returns -1, 0 or +1 as the field is below, at, or above the bound, along
// with what to call the quantity and the field's own value for the message.
func compare(v reflect.Value, param string) (cmp int, kind, got string, err error) {
	v = deref(v)

	switch v.Kind() {
	case reflect.String, reflect.Slice, reflect.Array, reflect.Map:
		bound, err := strconv.ParseInt(param, 10, 64)
		if err != nil {
			return 0, "", "", err
		}
		n := int64(v.Len())
		return cmpOrdered(n, bound), "length", strconv.FormatInt(n, 10), nil

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		bound, err := strconv.ParseInt(param, 10, 64)
		if err != nil {
			return 0, "", "", err
		}
		n := v.Int()
		return cmpOrdered(n, bound), "value", strconv.FormatInt(n, 10), nil

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n := v.Uint()
		bound, err := strconv.ParseUint(param, 10, 64)
		if err != nil {
			// A negative bound does not fit a uint64 but is still meaningful:
			// every unsigned value is above it.
			if signed, sErr := strconv.ParseInt(param, 10, 64); sErr == nil && signed < 0 {
				return 1, "value", strconv.FormatUint(n, 10), nil
			}
			return 0, "", "", err
		}
		return cmpOrdered(n, bound), "value", strconv.FormatUint(n, 10), nil

	case reflect.Float32, reflect.Float64:
		bound, err := strconv.ParseFloat(param, 64)
		if err != nil {
			return 0, "", "", err
		}
		n := v.Float()
		return cmpOrdered(n, bound), "value", strconv.FormatFloat(n, 'f', -1, 64), nil

	default:
		// A bool, a struct, a channel: there is no number here to bound. Saying
		// so names the mistake, where comparing against a fabricated zero would
		// report "value must be at least 1, got 0" about a field that has no
		// value in that sense at all.
		return 0, "", "", errUnmeasurable
	}
}

// errUnmeasurable reports a min/max/len rule on a kind that has neither a
// length nor a numeric value.
var errUnmeasurable = errors.New("not measurable")

func cmpOrdered[T int64 | uint64 | float64](a, b T) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func deref(v reflect.Value) reflect.Value {
	for v.Kind() == reflect.Pointer && !v.IsNil() {
		v = v.Elem()
	}
	return v
}

func isZero(v reflect.Value) bool {
	if v.Kind() == reflect.Pointer {
		return v.IsNil()
	}
	return v.IsZero()
}

// looksLikeEmail is a shape check, not RFC 5322. Anything stricter either
// rejects valid addresses or needs a parser; the only real proof an address
// works is sending to it.
func looksLikeEmail(s string) bool {
	local, domain, found := strings.Cut(s, "@")
	if !found || local == "" || domain == "" {
		return false
	}
	if strings.ContainsAny(s, " \t\r\n") || strings.Contains(domain, "@") {
		return false
	}
	dot := strings.LastIndexByte(domain, '.')
	return dot > 0 && dot < len(domain)-1
}
