package cfgx

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/example/cfgx/internal/ir"
)

// Schema is the runtime representation of a set of config entries and
// schema-level constraints. Build one with Define.
type Schema struct {
	entries     []Entry
	constraints []Constraint
}

// Constraint is a schema-level rule spanning several entries, such as
// MutuallyExclusive. Pass it to Define alongside entries.
type Constraint struct {
	kind ir.ConstraintType
	ids  []string
}

func (Constraint) isItem() {}

// MutuallyExclusive returns a Constraint that makes validation fail if more than
// one of the named entries has a non-zero value. IDs are dot-separated paths from
// the schema root (e.g. "db.host"). It panics if fewer than two IDs are given.
func MutuallyExclusive(ids ...string) Constraint {
	if len(ids) < 2 {
		panic(fmt.Sprintf("cfgx: MutuallyExclusive requires at least two IDs, got %d", len(ids)))
	}
	return Constraint{kind: ir.ConstraintMutuallyExclusive, ids: append([]string(nil), ids...)}
}

// ValueSource provides raw config values by dot-separated key. *viper.Viper
// satisfies it.
type ValueSource interface {
	Get(key string) any
	IsSet(key string) bool
}

// WatchEntry describes an IsLive entry for the runtime Watcher.
type WatchEntry struct {
	// Key is the dot-separated path of the entry, e.g. "db.host".
	Key string
	// Type is the entry's IR type.
	Type ir.EntryType
	// Env is the bound environment variable, if any.
	Env string
	// Getter lazily loads the value; nil if the entry has no Getter.
	Getter func() (any, error)
	// Compute derives the value from its parent config; set only for Computed entries.
	Compute func(parent any) any
	// OnChange is invoked with the old and new values; nil if no callback is set.
	OnChange func(old, new any)
}

// ValidationErrors is a multi-error returned by Validate.
type ValidationErrors struct {
	Errors []FieldError
}

// Error reports the number of failures followed by each one.
func (e *ValidationErrors) Error() string {
	msgs := make([]string, len(e.Errors))
	for i, fe := range e.Errors {
		msgs[i] = fe.Error()
	}
	noun := "errors"
	if len(e.Errors) == 1 {
		noun = "error"
	}
	return fmt.Sprintf("%d config validation %s: %s", len(e.Errors), noun, strings.Join(msgs, "; "))
}

// Unwrap returns each FieldError so errors.Is and errors.As can inspect them.
func (e *ValidationErrors) Unwrap() []error {
	errs := make([]error, len(e.Errors))
	for i, fe := range e.Errors {
		errs[i] = fe
	}
	return errs
}

// FieldError is a single validation failure for one config entry.
type FieldError struct {
	Field   string // dot-separated path, e.g. "db.password"
	Message string
}

// Error formats the failure as "field: message".
func (e FieldError) Error() string {
	return e.Field + ": " + e.Message
}

// ToIR converts the Schema to its serializable intermediate representation.
// Closures (Validate, OnChange, Getter) are not included; they are noted as
// HasValidate/HasOnChange/HasGetter bool flags in EntryIR.
func (s *Schema) ToIR() *ir.SchemaIR {
	out := &ir.SchemaIR{Version: ir.Version, Entries: make([]ir.EntryIR, 0, len(s.entries))}
	for _, e := range s.entries {
		out.Entries = append(out.Entries, e.toIR())
	}
	for _, c := range s.constraints {
		out.Constraints = append(out.Constraints, ir.ConstraintIR{
			Type:     c.kind,
			EntryIDs: append([]string(nil), c.ids...),
		})
	}
	return out
}

// Validate checks the values in v against the schema, in the order
// DependsOn gating, Required checks, validators (in declaration order), then
// MutuallyExclusive constraints. Values are converted to each entry's Go type
// first; a conversion failure is reported as a FieldError and that entry's
// validators are skipped. Validators run only for entries that are set.
//
// Validate returns a *ValidationErrors holding every failure, or nil on success.
// Called by generated Load() functions.
func (s *Schema) Validate(v ValueSource) error {
	if v == nil {
		return errors.New("cfgx: Validate called with nil ValueSource")
	}
	vc := &validation{src: v, index: map[string]Entry{}}
	indexEntries(vc.index, s.entries, "")
	for _, e := range s.entries {
		e.validate(vc, e.id())
	}
	for _, c := range s.constraints {
		vc.checkConstraint(c)
	}
	if len(vc.errs) == 0 {
		return nil
	}
	return &ValidationErrors{Errors: vc.errs}
}

// WatcherEntries returns the list of IsLive entries suitable for the runtime Watcher.
// Called by generated Load() to set up the IsLive watcher.
func (s *Schema) WatcherEntries() []WatchEntry {
	var out []WatchEntry
	for _, e := range s.entries {
		e.watch(e.id(), &out)
	}
	return out
}

func indexEntries(index map[string]Entry, entries []Entry, parent string) {
	for _, e := range entries {
		p := joinPath(parent, e.id())
		index[p] = e
		if o, ok := e.(*ObjectEntry); ok {
			indexEntries(index, o.properties, p)
		}
	}
}

// validation carries state for a single Schema.Validate run.
type validation struct {
	src   ValueSource
	index map[string]Entry
	errs  []FieldError
}

func (vc *validation) add(path, msg string) {
	vc.errs = append(vc.errs, FieldError{Field: path, Message: msg})
}

// redacted replaces sensitive values in error messages.
const redacted = "[redacted]"

// redactFrom rewrites the messages of vc.errs[start:] so that none contains the
// string form (quoted or bare) of any of vals.
func (vc *validation) redactFrom(start int, vals ...any) {
	for i := start; i < len(vc.errs); i++ {
		msg := vc.errs[i].Message
		for _, v := range vals {
			s := fmt.Sprint(v)
			if s == "" {
				continue
			}
			msg = strings.ReplaceAll(msg, strconv.Quote(s), redacted)
			msg = strings.ReplaceAll(msg, s, redacted)
		}
		vc.errs[i].Message = msg
	}
}

// resolve maps a referenced ID to a full key: a sibling of path if one exists,
// otherwise the ID is taken as a path from the schema root.
func (vc *validation) resolve(id, path string) string {
	if i := strings.LastIndex(path, "."); i >= 0 {
		if sib := path[:i] + "." + id; vc.index[sib] != nil {
			return sib
		}
	}
	return id
}

// value reads key from the source, converted to its entry's type when possible.
func (vc *validation) value(key string) any {
	raw := vc.src.Get(key)
	if ec, ok := vc.index[key].(elementChecker); ok {
		if v, err := ec.convertAny(raw); err == nil {
			return v
		}
	}
	return raw
}

// active reports whether an entry at path with the given DependsOn rule is active.
func (vc *validation) active(dep *ir.DependsOnIR, path string) bool {
	if dep == nil {
		return true
	}
	got := vc.value(vc.resolve(dep.ID, path))
	return reflect.DeepEqual(got, dep.Value) || fmt.Sprint(got) == fmt.Sprint(dep.Value)
}

func (vc *validation) checkConstraint(c Constraint) {
	if c.kind != ir.ConstraintMutuallyExclusive {
		return
	}
	var set []string
	for _, id := range c.ids {
		if vc.src.IsSet(id) && !isZeroValue(vc.value(id)) {
			set = append(set, id)
		}
	}
	if len(set) > 1 {
		vc.add(strings.Join(set, ", "), fmt.Sprintf(
			"mutually exclusive: only one of %s may be set", strings.Join(c.ids, ", ")))
	}
}

func isZeroValue(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Map, reflect.Array, reflect.String:
		return rv.Len() == 0
	default:
		return rv.IsZero()
	}
}

// ---------------------------------------------------------------------------
// Value conversion
// ---------------------------------------------------------------------------

// convertScalar converts an untyped source value (e.g. an env string) to T.
// A nil value converts to the zero value.
func convertScalar[T scalar](raw any) (T, error) {
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

// toList converts a source value to []any. Slices and arrays are copied
// element-wise; a string is split on commas with surrounding space trimmed.
func toList(raw any) ([]any, error) {
	if raw == nil {
		return nil, nil
	}
	if s, ok := raw.(string); ok {
		if strings.TrimSpace(s) == "" {
			return []any{}, nil
		}
		parts := strings.Split(s, ",")
		out := make([]any, len(parts))
		for i, p := range parts {
			out[i] = strings.TrimSpace(p)
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
