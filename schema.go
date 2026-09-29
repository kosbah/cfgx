package cfgx

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/epistemer/cfgx/internal/ir"
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
// Validate checks every entry regardless of its Scope; see ValidateScope to
// check only the entries one command uses.
//
// Validate returns a *ValidationErrors holding every failure, or nil on success.
func (s *Schema) Validate(v ValueSource) error {
	return s.validate(v, nil)
}

// ValidateScope is like Validate but checks only the entries in view for the
// command scope: entries without a Scope plus entries whose Scope includes
// scope. An Object's Scope applies to all of its properties, and a property
// with its own Scope must also include scope. With scope "" only the entries
// without a Scope are checked. A MutuallyExclusive constraint is checked only
// if all of its entries are in view.
//
// Called by generated Load functions: Load uses scope "" and Load<Scope> the
// scope of its command.
func (s *Schema) ValidateScope(v ValueSource, scope string) error {
	return s.validate(v, &scope)
}

// validate runs a validation; a nil scope means every entry is in view.
func (s *Schema) validate(v ValueSource, scope *string) error {
	if v == nil {
		return errors.New("cfgx: Validate called with nil ValueSource")
	}
	vc := &validation{src: v, index: map[string]Entry{}}
	indexEntries(vc.index, s.entries, "")
	if scope != nil {
		vc.hidden = map[string]bool{}
		hideOutOfScope(vc.hidden, s.entries, "", *scope, false)
	}
	for _, e := range s.entries {
		if vc.hidden[e.id()] {
			continue
		}
		e.validate(vc, e.id())
	}
	for _, c := range s.constraints {
		if vc.inView(c.ids...) {
			vc.checkConstraint(c)
		}
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

// Getter returns the Getter function of the entry at key, a dot-separated path
// such as "db.password", with its result boxed as any. It returns nil if there
// is no such entry or the entry has no Getter. Computed entries have no Getter;
// use Compute for them. Called by generated Load() functions.
func (s *Schema) Getter(key string) func() (any, error) {
	if g, ok := s.lookup(key).(getterEntry); ok {
		return g.getterAny()
	}
	return nil
}

// Compute evaluates the Computed entry at key, a dot-separated path such as
// "db.dsn", passing parent to its function. parent is the generated struct
// that contains the entry (e.g. *DbConfig). Compute returns an error if key
// does not name a Computed entry. Called by generated getters.
func (s *Schema) Compute(key string, parent any) (any, error) {
	e := s.lookup(key)
	if c, ok := e.(computedEntry); ok {
		return c.computeAny(parent), nil
	}
	if e == nil {
		return nil, fmt.Errorf("cfgx: Compute: no entry %q", key)
	}
	return nil, fmt.Errorf("cfgx: Compute: entry %q is not a Computed entry", key)
}

// getterEntry is implemented by entries that may carry a Getter.
type getterEntry interface {
	getterAny() func() (any, error)
}

// computedEntry is implemented by Computed entries.
type computedEntry interface {
	computeAny(parent any) any
}

// lookup returns the entry at the dot-separated path key, or nil.
func (s *Schema) lookup(key string) Entry {
	entries := s.entries
	for {
		id, rest, nested := strings.Cut(key, ".")
		var found Entry
		for _, e := range entries {
			if e.id() == id {
				found = e
				break
			}
		}
		if found == nil || !nested {
			return found
		}
		o, ok := found.(*ObjectEntry)
		if !ok {
			return nil
		}
		entries, key = o.properties, rest
	}
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

// hideOutOfScope records in hidden the path of every entry that is not in
// view for scope: an entry whose Scope is set but does not include scope, and
// everything below it. parentHidden marks entries whose parent is hidden.
func hideOutOfScope(hidden map[string]bool, entries []Entry, parent, scope string, parentHidden bool) {
	for _, e := range entries {
		p := joinPath(parent, e.id())
		h := parentHidden || !inScope(e.scopes(), scope)
		if h {
			hidden[p] = true
		}
		if o, ok := e.(*ObjectEntry); ok {
			hideOutOfScope(hidden, o.properties, p, scope, h)
		}
	}
}

// inScope reports whether an entry with the given Scope list is in view for
// scope. Entries without a Scope are always in view.
func inScope(scopes []string, scope string) bool {
	if len(scopes) == 0 {
		return true
	}
	for _, s := range scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// validation carries state for a single Schema.Validate run.
type validation struct {
	src    ValueSource
	index  map[string]Entry
	hidden map[string]bool // paths of entries out of view; nil means all are in view
	errs   []FieldError
}

// inView reports whether every one of the given paths is in view.
func (vc *validation) inView(paths ...string) bool {
	for _, p := range paths {
		if vc.hidden[p] {
			return false
		}
	}
	return true
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
