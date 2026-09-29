package runtime

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/epistemer/cfgx"
	"github.com/epistemer/cfgx/internal/convert"
	"github.com/epistemer/cfgx/internal/ir"
	"github.com/epistemer/cfgx/internal/validate"
)

// ValidatorFunc validates a value. The argument is normally the Go type backing
// the config entry (string for string entries, int for int entries, and so on);
// built-in validators also accept any value convertible to that type, such as
// "8080" for Port.
type ValidatorFunc func(val any) error

// Registry holds named validator functions. It is safe for concurrent use.
type Registry struct {
	mu         sync.RWMutex
	validators map[string]ValidatorFunc
}

// DefaultRegistry is pre-populated with the parameterless built-in validators
// under their SchemaIR names: "email", "url", "port", "base64", "duration" and
// "semver". The parameterized built-ins ("port_range", "regex", "enum",
// "range") are constructed from ValidatorIR.Params by ValidateAll.
var DefaultRegistry = NewRegistry()

func init() {
	DefaultRegistry.Register("email", Email())
	DefaultRegistry.Register("url", URL())
	DefaultRegistry.Register("port", Port())
	DefaultRegistry.Register("base64", Base64())
	DefaultRegistry.Register("duration", Duration())
	DefaultRegistry.Register("semver", Semver())
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{validators: map[string]ValidatorFunc{}}
}

// Register adds a named validator. It panics if name is empty, fn is nil or
// name is already registered.
func (r *Registry) Register(name string, fn ValidatorFunc) {
	if name == "" {
		panic("cfgx/runtime: Register called with empty name")
	}
	if fn == nil {
		panic(fmt.Sprintf("cfgx/runtime: Register(%q) called with nil function", name))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.validators[name]; dup {
		panic(fmt.Sprintf("cfgx/runtime: validator %q already registered", name))
	}
	r.validators[name] = fn
}

// Get returns the validator registered under name, or nil if there is none.
func (r *Registry) Get(name string) ValidatorFunc {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.validators[name]
}

// ValidateAll runs DefaultRegistry.ValidateAll.
func ValidateAll(entries []ir.EntryIR, v cfgx.ValueSource) error {
	return DefaultRegistry.ValidateAll(entries, v)
}

// ValidateAll runs the built-in validators of every entry against the values in
// v. It walks Object properties using dot-separated keys, skips keys that are
// not set, converts each value to the entry's type and, for List entries, also
// runs the element type's validators on every element (keys "hosts[0]", ...).
//
// Parameterized validators ("port_range", "regex", "enum", "range") are built
// from ValidatorIR.Params; all other names are looked up in r. Custom closures
// (EntryIR.HasValidate) are not available in IR and are not run, nor are
// Required, DependsOn or schema constraints; use cfgx.Schema.Validate for those.
//
// It returns a *cfgx.ValidationErrors listing every failure, or nil.
func (r *Registry) ValidateAll(entries []ir.EntryIR, v cfgx.ValueSource) error {
	if v == nil {
		return fmt.Errorf("cfgx/runtime: ValidateAll called with nil ValueSource")
	}
	var errs []cfgx.FieldError
	r.validateEntries(entries, "", v, &errs)
	if len(errs) == 0 {
		return nil
	}
	return &cfgx.ValidationErrors{Errors: errs}
}

func (r *Registry) validateEntries(entries []ir.EntryIR, parent string, v cfgx.ValueSource, errs *[]cfgx.FieldError) {
	for i := range entries {
		e := &entries[i]
		key := e.ID
		if parent != "" {
			key = parent + "." + e.ID
		}
		switch e.Type {
		case ir.TypeObject:
			r.validateEntries(e.Properties, key, v, errs)
			continue
		case ir.TypeComputed:
			continue
		}
		if !v.IsSet(key) {
			continue
		}
		val, err := convert.ToType(e.Type, v.Get(key))
		if err != nil {
			addErr(errs, e, key, fmt.Sprintf("invalid %s value: %v", e.Type, err), v.Get(key))
			continue
		}
		r.check(e, key, val, errs)
		if e.Type != ir.TypeList || e.ElementType == nil {
			continue
		}
		list, ok := val.([]any)
		if !ok {
			continue
		}
		for j, raw := range list {
			ekey := key + "[" + strconv.Itoa(j) + "]"
			ev, err := convert.ToType(e.ElementType.Type, raw)
			if err != nil {
				addErr(errs, e, ekey, fmt.Sprintf("invalid element value: %v", err), raw)
				continue
			}
			r.checkWith(e, e.ElementType.BuiltinValidators, ekey, ev, errs)
		}
	}
}

func (r *Registry) check(e *ir.EntryIR, key string, val any, errs *[]cfgx.FieldError) {
	r.checkWith(e, e.BuiltinValidators, key, val, errs)
}

// checkWith runs vals against val, reporting failures under key. Messages of
// sensitive entries have the value redacted.
func (r *Registry) checkWith(e *ir.EntryIR, vals []ir.ValidatorIR, key string, val any, errs *[]cfgx.FieldError) {
	for _, vir := range vals {
		fn, err := r.build(vir)
		if err != nil {
			*errs = append(*errs, cfgx.FieldError{Field: key, Message: err.Error()})
			continue
		}
		if err := fn(val); err != nil {
			addErr(errs, e, key, err.Error(), val)
		}
	}
}

const redacted = "[redacted]"

func addErr(errs *[]cfgx.FieldError, e *ir.EntryIR, key, msg string, val any) {
	if e.Sensitive {
		if s := fmt.Sprint(val); s != "" {
			msg = strings.ReplaceAll(msg, strconv.Quote(s), redacted)
			msg = strings.ReplaceAll(msg, s, redacted)
		}
	}
	*errs = append(*errs, cfgx.FieldError{Field: key, Message: msg})
}

// build returns the ValidatorFunc for vir, constructing parameterized
// built-ins from vir.Params. Invalid params are reported as an error instead
// of the constructor's panic.
func (r *Registry) build(vir ir.ValidatorIR) (fn ValidatorFunc, err error) {
	defer func() {
		if p := recover(); p != nil {
			fn, err = nil, fmt.Errorf("invalid %s validator: %v", vir.Name, p)
		}
	}()
	switch vir.Name {
	case "port_range":
		lo, err1 := convert.To[int](vir.Params["min"])
		hi, err2 := convert.To[int](vir.Params["max"])
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("invalid port_range params %v", vir.Params)
		}
		return PortRange(lo, hi), nil
	case "range":
		lo, err1 := convert.To[float64](vir.Params["min"])
		hi, err2 := convert.To[float64](vir.Params["max"])
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("invalid range params %v", vir.Params)
		}
		return Range(lo, hi), nil
	case "regex":
		p, ok := vir.Params["pattern"].(string)
		if !ok {
			return nil, fmt.Errorf("invalid regex params %v", vir.Params)
		}
		return Regex(p), nil
	case "enum":
		list, err := convert.ToList(vir.Params["values"])
		if err != nil {
			return nil, fmt.Errorf("invalid enum params %v", vir.Params)
		}
		vals := make([]string, len(list))
		for i, x := range list {
			if vals[i], err = convert.To[string](x); err != nil {
				return nil, fmt.Errorf("invalid enum params %v", vir.Params)
			}
		}
		return Enum(vals...), nil
	}
	if fn := r.Get(vir.Name); fn != nil {
		return fn, nil
	}
	return nil, fmt.Errorf("unknown validator %q", vir.Name)
}

// typed adapts a typed validator to a ValidatorFunc, converting the argument
// to T first.
func typed[T convert.Scalar](fn func(T) error) ValidatorFunc {
	return func(val any) error {
		v, err := convert.To[T](val)
		if err != nil {
			return err
		}
		return fn(v)
	}
}

// Email returns a ValidatorFunc that accepts a bare RFC 5322 email address
// (checked with net/mail.ParseAddress).
func Email() ValidatorFunc { return typed(validate.Email()) }

// URL returns a ValidatorFunc that accepts values url.Parse can parse and that
// have a scheme.
func URL() ValidatorFunc { return typed(validate.URL()) }

// Port returns a ValidatorFunc that checks 1 <= v <= 65535.
func Port() ValidatorFunc { return typed(validate.Port()) }

// PortRange returns a ValidatorFunc that checks min <= v <= max.
// It panics if min < 1, max > 65535 or min > max.
func PortRange(min, max int) ValidatorFunc { return typed(validate.PortRange(min, max)) }

// Base64 returns a ValidatorFunc that accepts padded base64 in the standard or
// URL-safe alphabet.
func Base64() ValidatorFunc { return typed(validate.Base64()) }

// Regex returns a ValidatorFunc that checks whether v matches pattern. The
// pattern is compiled at construction time; Regex panics if it is invalid.
func Regex(pattern string) ValidatorFunc { return typed(validate.Regex(pattern)) }

// Duration returns a ValidatorFunc that accepts strings parseable by
// time.ParseDuration. A time.Duration value is always accepted.
func Duration() ValidatorFunc {
	fn := typed(validate.Duration())
	return func(val any) error {
		if _, ok := val.(time.Duration); ok {
			return nil
		}
		return fn(val)
	}
}

// Semver returns a ValidatorFunc that accepts X.Y.Z semantic versions, with or
// without a leading "v". The patch version is required.
func Semver() ValidatorFunc { return typed(validate.Semver()) }

// Enum returns a ValidatorFunc that checks whether v is one of values,
// compared case-insensitively.
func Enum(values ...string) ValidatorFunc { return typed(validate.Enum(values...)) }

// Range returns a ValidatorFunc that checks min <= v <= max for float64 values.
func Range(min, max float64) ValidatorFunc { return typed(validate.Range(min, max)) }
