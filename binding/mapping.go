package binding

import (
	"fmt"
	"reflect"
	"strconv"
	"time"
)

// source yields the values submitted under a key, and whether the key was
// present at all. Every non-JSON binder is this function plus a struct tag,
// which is what lets them share one mapper — and lets Query and Form read
// through the Context's own parse caches rather than re-parsing.
type source func(key string) ([]string, bool)

// mapSource fills a struct from a source, matching fields by tag.
//
// Reflection is fine here: this package is an optional import and never runs
// in the core request path. It stays out of gomicro itself for exactly that
// reason.
func mapSource(obj any, tag string, get source) error {
	v := reflect.ValueOf(obj)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return fmt.Errorf("binding: target must be a non-nil pointer, got %T", obj)
	}
	v = v.Elem()
	if v.Kind() != reflect.Struct {
		return fmt.Errorf("binding: target must point to a struct, got %T", obj)
	}
	return mapStruct(v, tag, get)
}

func mapStruct(v reflect.Value, tag string, get source) error {
	t := v.Type()
	for i := range t.NumField() {
		field, value := t.Field(i), v.Field(i)
		if !field.IsExported() {
			continue
		}

		name, ok := field.Tag.Lookup(tag)
		if name == "-" {
			continue
		}
		if !ok || name == "" {
			name = field.Name
		}

		// An embedded or nested struct without its own tag is flattened: its
		// fields are looked up by their own names, not prefixed.
		if !ok && isPlainStruct(field.Type) {
			if err := mapStruct(value, tag, get); err != nil {
				return err
			}
			continue
		}

		values, present := get(name)
		if !present || len(values) == 0 {
			continue
		}
		if err := setValue(value, values); err != nil {
			return fmt.Errorf("binding: field %s: %w", field.Name, err)
		}
	}
	return nil
}

// isPlainStruct reports whether t is a struct we should descend into rather
// than parse. time.Time is a struct but is a leaf value.
func isPlainStruct(t reflect.Type) bool {
	return t.Kind() == reflect.Struct && t != reflect.TypeOf(time.Time{})
}

func setValue(v reflect.Value, values []string) error {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		return setValue(v.Elem(), values)

	case reflect.Slice:
		slice := reflect.MakeSlice(v.Type(), len(values), len(values))
		for i, s := range values {
			if err := setScalar(slice.Index(i), s); err != nil {
				return err
			}
		}
		v.Set(slice)
		return nil

	default:
		return setScalar(v, values[0])
	}
}

func setScalar(v reflect.Value, s string) error {
	// time.Duration is an int64 with its own parser, so it comes first.
	if v.Type() == reflect.TypeOf(time.Duration(0)) {
		d, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("%q is not a duration", s)
		}
		v.SetInt(int64(d))
		return nil
	}
	if v.Type() == reflect.TypeOf(time.Time{}) {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return fmt.Errorf("%q is not an RFC 3339 time", s)
		}
		v.Set(reflect.ValueOf(t))
		return nil
	}

	switch v.Kind() {
	case reflect.String:
		v.SetString(s)

	case reflect.Bool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return fmt.Errorf("%q is not a bool", s)
		}
		v.SetBool(b)

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(s, 10, v.Type().Bits())
		if err != nil {
			return fmt.Errorf("%q is not an integer", s)
		}
		v.SetInt(n)

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(s, 10, v.Type().Bits())
		if err != nil {
			return fmt.Errorf("%q is not an unsigned integer", s)
		}
		v.SetUint(n)

	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(s, v.Type().Bits())
		if err != nil {
			return fmt.Errorf("%q is not a number", s)
		}
		v.SetFloat(f)

	case reflect.Pointer:
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		return setScalar(v.Elem(), s)

	default:
		return fmt.Errorf("unsupported type %s", v.Type())
	}
	return nil
}
