// Package convert turns untyped config values (env strings, viper values,
// JSON-decoded IR literals) into the Go types backing cfgx entries. It is
// shared by the root cfgx package and the runtime package so that Load() and
// the CLI convert values identically.
package convert

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/example/cfgx/internal/ir"
)

// Scalar is the set of Go types backing the scalar entry kinds.
type Scalar interface {
	string | int | bool | float64 | time.Duration
}

// To converts an untyped source value (e.g. an env string) to T.
// A nil value converts to the zero value.
func To[T Scalar](raw any) (T, error) {
	var zero T
	if raw == nil {
		return zero, nil
	}
	if v, ok := raw.(T); ok {
		return v, nil
	}
	var (
		out any
		err error
	)
	switch any(zero).(type) {
	case string:
		out, err = toString(raw)
	case int:
		out, err = toInt(raw)
	case bool:
		out, err = toBool(raw)
	case float64:
		out, err = toFloat(raw)
	case time.Duration:
		out, err = toDuration(raw)
	}
	if err != nil {
		return zero, err
	}
	v, ok := out.(T)
	if !ok {
		return zero, fmt.Errorf("cannot convert %T to %T", raw, zero)
	}
	return v, nil
}

// ToType converts raw to the Go type backing the IR type t: string, int,
// bool, float64, time.Duration, or []any for lists. Object and computed
// values are returned unchanged.
func ToType(t ir.EntryType, raw any) (any, error) {
	switch t {
	case ir.TypeString:
		return To[string](raw)
	case ir.TypeInt:
		return To[int](raw)
	case ir.TypeBool:
		return To[bool](raw)
	case ir.TypeFloat64:
		return To[float64](raw)
	case ir.TypeDuration:
		return To[time.Duration](raw)
	case ir.TypeList:
		return ToList(raw)
	}
	return raw, nil
}

// Like converts raw to the dynamic type of sample, which must be one of the
// Scalar types or []any. If sample is nil or of another type, raw is returned
// unchanged.
func Like(sample, raw any) (any, error) {
	switch sample.(type) {
	case string:
		return To[string](raw)
	case int:
		return To[int](raw)
	case bool:
		return To[bool](raw)
	case float64:
		return To[float64](raw)
	case time.Duration:
		return To[time.Duration](raw)
	case []any:
		return ToList(raw)
	}
	return raw, nil
}

// ToList converts a source value to []any. Slices and arrays are copied
// element-wise; a string is split on commas and whitespace, dropping empty parts.
func ToList(raw any) ([]any, error) {
	if raw == nil {
		return nil, nil
	}
	if s, ok := raw.(string); ok {
		// Split on commas and whitespace, so both "a,b" and viper's
		// space-separated env form "a b" (as written in .env.example) work.
		parts := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
		out := make([]any, len(parts))
		for i, p := range parts {
			out[i] = p
		}
		return out, nil
	}
	rv := reflect.ValueOf(raw)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return nil, fmt.Errorf("cannot convert %T to list", raw)
	}
	out := make([]any, rv.Len())
	for i := range out {
		out[i] = rv.Index(i).Interface()
	}
	return out, nil
}

func toString(raw any) (string, error) {
	switch v := raw.(type) {
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	case fmt.Stringer:
		return v.String(), nil
	}
	switch reflect.ValueOf(raw).Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64, reflect.String:
		return fmt.Sprint(raw), nil
	}
	return "", fmt.Errorf("cannot convert %T to string", raw)
}

func toInt(raw any) (int, error) {
	if s, ok := raw.(string); ok {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil {
			return 0, fmt.Errorf("%q is not an integer", s)
		}
		return n, nil
	}
	rv := reflect.ValueOf(raw)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return int(rv.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int(rv.Uint()), nil
	case reflect.Float32, reflect.Float64:
		f := rv.Float()
		if f != float64(int(f)) {
			return 0, fmt.Errorf("%v is not an integer", f)
		}
		return int(f), nil
	}
	return 0, fmt.Errorf("cannot convert %T to int", raw)
}

func toBool(raw any) (bool, error) {
	if s, ok := raw.(string); ok {
		b, err := strconv.ParseBool(strings.TrimSpace(s))
		if err != nil {
			return false, fmt.Errorf("%q is not a boolean", s)
		}
		return b, nil
	}
	rv := reflect.ValueOf(raw)
	switch rv.Kind() {
	case reflect.Bool:
		return rv.Bool(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int() != 0, nil
	}
	return false, fmt.Errorf("cannot convert %T to bool", raw)
}

func toFloat(raw any) (float64, error) {
	if s, ok := raw.(string); ok {
		f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return 0, fmt.Errorf("%q is not a number", s)
		}
		return f, nil
	}
	rv := reflect.ValueOf(raw)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(rv.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(rv.Uint()), nil
	case reflect.Float32, reflect.Float64:
		return rv.Float(), nil
	}
	return 0, fmt.Errorf("cannot convert %T to float64", raw)
}

func toDuration(raw any) (time.Duration, error) {
	if s, ok := raw.(string); ok {
		d, err := time.ParseDuration(strings.TrimSpace(s))
		if err != nil {
			return 0, fmt.Errorf("%q is not a duration", s)
		}
		return d, nil
	}
	rv := reflect.ValueOf(raw)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return time.Duration(rv.Int()), nil
	}
	return 0, fmt.Errorf("cannot convert %T to duration", raw)
}
