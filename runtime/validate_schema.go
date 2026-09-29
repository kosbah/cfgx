package runtime

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/epistemer/cfgx"
	"github.com/epistemer/cfgx/internal/convert"
	"github.com/epistemer/cfgx/internal/ir"
)

// ValidateSchema checks the values in v against the schema IR s, in the same
// order as cfgx.Schema.Validate: for each entry, DependsOn gating, the
// Required check ("required but not set") and the built-in validators (via
// ValidateAll), then the MutuallyExclusive constraints.
//
// It works from the IR alone, so custom Validate closures are not run and
// entries with a Getter are skipped (their value is only known at run time).
// Computed entries are skipped as well.
//
// It returns a *cfgx.ValidationErrors listing every failure, or nil.
func ValidateSchema(s *ir.SchemaIR, v cfgx.ValueSource) error {
	if s == nil {
		return errors.New("cfgx/runtime: ValidateSchema called with nil schema")
	}
	if v == nil {
		return errors.New("cfgx/runtime: ValidateSchema called with nil ValueSource")
	}
	sv := &schemaValidation{src: v, index: map[string]*ir.EntryIR{}}
	indexIR(sv.index, s.Entries, "")
	sv.entries(s.Entries, nil)
	for _, c := range s.Constraints {
		sv.constraint(c)
	}
	if len(sv.errs) == 0 {
		return nil
	}
	return &cfgx.ValidationErrors{Errors: sv.errs}
}

type schemaValidation struct {
	src   cfgx.ValueSource
	index map[string]*ir.EntryIR
	errs  []cfgx.FieldError
}

func indexIR(index map[string]*ir.EntryIR, entries []ir.EntryIR, parent string) {
	for i := range entries {
		e := &entries[i]
		key := joinKey(parent, e.ID)
		index[key] = e
		if e.Type == ir.TypeObject {
			indexIR(index, e.Properties, key)
		}
	}
}

func joinKey(parent, id string) string {
	if parent == "" {
		return id
	}
	return parent + "." + id
}

// entries validates entries whose parent objects are given by chain (outermost
// first).
func (sv *schemaValidation) entries(entries []ir.EntryIR, chain []*ir.EntryIR) {
	parent := ""
	for _, p := range chain {
		parent = joinKey(parent, p.ID)
	}
	for i := range entries {
		e := &entries[i]
		key := joinKey(parent, e.ID)
		if !sv.active(e.DependsOn, key) {
			continue
		}
		switch e.Type {
		case ir.TypeObject:
			sv.entries(e.Properties, append(chain[:len(chain):len(chain)], e))
			continue
		case ir.TypeComputed:
			continue
		}
		if e.HasGetter {
			continue
		}
		if e.Required && sv.missing(e, key) {
			sv.errs = append(sv.errs, cfgx.FieldError{Field: key, Message: "required but not set"})
			continue
		}
		if !sv.src.IsSet(key) {
			continue
		}
		if err := ValidateAll(wrap(e, chain), sv.src); err != nil {
			var ve *cfgx.ValidationErrors
			if errors.As(err, &ve) {
				sv.errs = append(sv.errs, ve.Errors...)
			} else {
				sv.errs = append(sv.errs, cfgx.FieldError{Field: key, Message: err.Error()})
			}
		}
	}
}

// missing reports whether a required entry has no usable value: it is unset,
// or set to its type's zero value (an empty list for lists). A value that
// fails conversion is not missing; ValidateAll reports it.
func (sv *schemaValidation) missing(e *ir.EntryIR, key string) bool {
	if !sv.src.IsSet(key) {
		return true
	}
	val, err := convert.ToType(e.Type, sv.src.Get(key))
	if err != nil {
		return false
	}
	return isZero(val)
}

// wrap returns a one-entry tree holding e nested in copies of its parent
// objects, so ValidateAll reports it under its full key.
func wrap(e *ir.EntryIR, chain []*ir.EntryIR) []ir.EntryIR {
	out := []ir.EntryIR{*e}
	for i := len(chain) - 1; i >= 0; i-- {
		out = []ir.EntryIR{{ID: chain[i].ID, Type: ir.TypeObject, Properties: out}}
	}
	return out
}

// resolve maps a referenced ID to a full key: a sibling of key if one exists,
// otherwise the ID is taken as a path from the schema root.
func (sv *schemaValidation) resolve(id, key string) string {
	if i := strings.LastIndex(key, "."); i >= 0 {
		if sib := key[:i] + "." + id; sv.index[sib] != nil {
			return sib
		}
	}
	return id
}

// value reads key from the source, converted to its entry's type when possible.
func (sv *schemaValidation) value(key string) any {
	raw := sv.src.Get(key)
	if e := sv.index[key]; e != nil {
		if v, err := convert.ToType(e.Type, raw); err == nil {
			return v
		}
	}
	return raw
}

func (sv *schemaValidation) active(dep *ir.DependsOnIR, key string) bool {
	if dep == nil {
		return true
	}
	got := sv.value(sv.resolve(dep.ID, key))
	return reflect.DeepEqual(got, dep.Value) || fmt.Sprint(got) == fmt.Sprint(dep.Value)
}

func (sv *schemaValidation) constraint(c ir.ConstraintIR) {
	if c.Type != ir.ConstraintMutuallyExclusive {
		return
	}
	var set []string
	for _, id := range c.EntryIDs {
		if sv.src.IsSet(id) && !isZero(sv.value(id)) {
			set = append(set, id)
		}
	}
	if len(set) > 1 {
		sv.errs = append(sv.errs, cfgx.FieldError{
			Field:   strings.Join(set, ", "),
			Message: fmt.Sprintf("mutually exclusive: only one of %s may be set", strings.Join(c.EntryIDs, ", ")),
		})
	}
}

func isZero(v any) bool {
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
