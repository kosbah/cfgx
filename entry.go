package cfgx

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/example/cfgx/internal/convert"
	"github.com/example/cfgx/internal/ir"
)

// Item is anything that can be passed to Define: an Entry or a Constraint.
//
// Item has unexported methods, so it can only be implemented by this package.
type Item interface {
	isItem()
}

// Entry is a single config entry built with one of the DSL constructors
// (String, Int, Bool, Float64, Duration, List, Object or Computed).
//
// Entry has unexported methods, so it can only be implemented by this package.
type Entry interface {
	Item
	id() string
	scopes() []string
	toIR() ir.EntryIR
	validate(vc *validation, path string)
	watch(path string, out *[]WatchEntry)
}

// elementChecker is implemented by entries that can serve as a List element type
// with value conversion and per-element validation.
type elementChecker interface {
	convertAny(raw any) (any, error)
	checkAny(vc *validation, path string, v any)
}

// scalar is the set of Go types backing the scalar entry kinds.
type scalar interface {
	string | int | bool | float64 | time.Duration
}

// base holds the properties shared by every entry kind.
type base struct {
	entryID    string
	flagLong   string
	flagShort  string
	env        string
	desc       string
	required   bool
	sensitive  bool
	isLive     bool
	deprecated string
	scope      []string
	tags       []string
	dependsOn  *ir.DependsOnIR
}

func (b *base) isItem() {}

func (b *base) id() string { return b.entryID }

func (b *base) scopes() []string { return b.scope }

func (b *base) baseIR(t ir.EntryType) ir.EntryIR {
	e := ir.EntryIR{
		ID:          b.entryID,
		Type:        t,
		FlagLong:    b.flagLong,
		FlagShort:   b.flagShort,
		Env:         b.env,
		Description: b.desc,
		Required:    b.required,
		Sensitive:   b.sensitive,
		IsLive:      b.isLive,
		Deprecated:  b.deprecated,
		Scope:       copyStrings(b.scope),
		Tags:        copyStrings(b.tags),
	}
	if b.dependsOn != nil {
		d := *b.dependsOn
		e.DependsOn = &d
	}
	return e
}

func (b *base) setFlag(long string, short []string) {
	if len(short) > 1 {
		panic(fmt.Sprintf("cfgx: entry %q: Flag accepts at most one short flag, got %d", b.entryID, len(short)))
	}
	b.flagLong = long
	b.flagShort = ""
	if len(short) == 1 {
		b.flagShort = short[0]
	}
}

func copyStrings(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return append([]string(nil), s...)
}

func newBase(id string) base {
	if id == "" {
		panic("cfgx: entry ID must not be empty")
	}
	if strings.ContainsAny(id, ".[]") {
		panic(fmt.Sprintf("cfgx: entry ID %q must not contain '.', '[' or ']'; use Object for nesting", id))
	}
	return base{entryID: id}
}

// ---------------------------------------------------------------------------
// Scalar entries
// ---------------------------------------------------------------------------

// Field is the generic builder behind the scalar entry kinds. Use the typed
// aliases (StringEntry, IntEntry, BoolEntry, Float64Entry, DurationEntry) and
// their constructors rather than instantiating Field directly.
type Field[T scalar] struct {
	base
	typ        ir.EntryType
	hasDefault bool
	def        T
	validators []func(T) error
	onChange   func(old, new T)
	getter     func() (T, error)
}

// StringEntry is a string config entry.
type StringEntry = Field[string]

// IntEntry is an int config entry.
type IntEntry = Field[int]

// BoolEntry is a bool config entry.
type BoolEntry = Field[bool]

// Float64Entry is a float64 config entry.
type Float64Entry = Field[float64]

// DurationEntry is a time.Duration config entry.
type DurationEntry = Field[time.Duration]

// String declares a string config entry.
func String(id string) *StringEntry {
	return &StringEntry{base: newBase(id), typ: ir.TypeString}
}

// Int declares an int config entry.
func Int(id string) *IntEntry {
	return &IntEntry{base: newBase(id), typ: ir.TypeInt}
}

// Bool declares a bool config entry.
func Bool(id string) *BoolEntry {
	return &BoolEntry{base: newBase(id), typ: ir.TypeBool}
}

// Float64 declares a float64 config entry.
func Float64(id string) *Float64Entry {
	return &Float64Entry{base: newBase(id), typ: ir.TypeFloat64}
}

// Duration declares a time.Duration config entry. Its default is serialized in
// the IR as a duration string such as "30s".
func Duration(id string) *DurationEntry {
	return &DurationEntry{base: newBase(id), typ: ir.TypeDuration}
}

// Flag sets the cobra long flag (e.g. "--port") and an optional short flag (e.g. "-p").
func (f *Field[T]) Flag(long string, short ...string) *Field[T] {
	f.setFlag(long, short)
	return f
}

// Env sets the environment variable bound to this entry.
func (f *Field[T]) Env(name string) *Field[T] {
	f.env = name
	return f
}

// Default sets the default value.
func (f *Field[T]) Default(val T) *Field[T] {
	f.hasDefault = true
	f.def = val
	return f
}

// Required marks the entry as required: loading fails if it is unset or holds
// the zero value of its type. For a Bool entry this means the value must be
// true; for Int and Float64 it must be non-zero; for String it must be non-empty.
func (f *Field[T]) Required() *Field[T] {
	f.required = true
	return f
}

// Sensitive marks the entry as a secret that must be redacted in output.
func (f *Field[T]) Sensitive() *Field[T] {
	f.sensitive = true
	return f
}

// Desc sets the human-readable description.
func (f *Field[T]) Desc(text string) *Field[T] {
	f.desc = text
	return f
}

// Validate appends a validator. Validators run in declaration order.
func (f *Field[T]) Validate(fn func(T) error) *Field[T] {
	if fn == nil {
		panic(fmt.Sprintf("cfgx: entry %q: Validate called with nil function", f.entryID))
	}
	f.validators = append(f.validators, fn)
	return f
}

// IsLive marks the entry as hot-reloadable.
func (f *Field[T]) IsLive() *Field[T] {
	f.isLive = true
	return f
}

// OnChange sets the callback invoked when a live value changes.
func (f *Field[T]) OnChange(fn func(old, new T)) *Field[T] {
	f.onChange = fn
	return f
}

// Getter sets a lazy-load function used instead of viper to obtain the value.
func (f *Field[T]) Getter(fn func() (T, error)) *Field[T] {
	f.getter = fn
	return f
}

// Deprecated marks the entry as deprecated with the given message.
func (f *Field[T]) Deprecated(message string) *Field[T] {
	f.deprecated = message
	return f
}

// Scope restricts the entry to the given cobra commands.
func (f *Field[T]) Scope(commands ...string) *Field[T] {
	f.scope = copyStrings(commands)
	return f
}

// Tags sets documentation grouping labels.
func (f *Field[T]) Tags(tags ...string) *Field[T] {
	f.tags = copyStrings(tags)
	return f
}

// DependsOn activates validation of this entry only when the entry id equals value.
func (f *Field[T]) DependsOn(id string, value any) *Field[T] {
	f.dependsOn = &ir.DependsOnIR{ID: id, Value: value}
	return f
}

func (f *Field[T]) toIR() ir.EntryIR {
	e := f.baseIR(f.typ)
	if f.hasDefault {
		e.Default = scalarDefaultIR(f.def)
	}
	e.HasValidate = len(f.validators) > 0
	e.HasOnChange = f.onChange != nil
	e.HasGetter = f.getter != nil
	return e
}

// scalarDefaultIR renders a default as a JSON-compatible literal (invariant 7).
func scalarDefaultIR(v any) any {
	if d, ok := v.(time.Duration); ok {
		return d.String()
	}
	return v
}

func (f *Field[T]) value(vc *validation, path string) (val T, set bool, err error) {
	if f.getter != nil {
		v, gerr := f.getter()
		if gerr != nil {
			return val, false, fmt.Errorf("getter failed: %w", gerr)
		}
		return v, true, nil
	}
	if !vc.src.IsSet(path) {
		return val, false, nil
	}
	v, cerr := convert.To[T](vc.src.Get(path))
	if cerr != nil {
		if f.sensitive {
			// Do not echo the source value of a secret.
			return val, false, fmt.Errorf("invalid %s value %s", f.typ, redacted)
		}
		return val, false, fmt.Errorf("invalid %s value: %w", f.typ, cerr)
	}
	return v, true, nil
}

func (f *Field[T]) validate(vc *validation, path string) {
	if !vc.active(f.dependsOn, path) {
		return
	}
	val, set, err := f.value(vc, path)
	if err != nil {
		vc.add(path, err.Error())
		return
	}
	var zero T
	if f.required && (!set || val == zero) {
		vc.add(path, "required")
		return
	}
	if !set {
		return
	}
	start := len(vc.errs)
	f.check(vc, path, val)
	if f.sensitive {
		vc.redactFrom(start, val)
	}
}

func (f *Field[T]) check(vc *validation, path string, val T) {
	for _, fn := range f.validators {
		if err := fn(val); err != nil {
			vc.add(path, err.Error())
		}
	}
}

func (f *Field[T]) convertAny(raw any) (any, error) {
	v, err := convert.To[T](raw)
	if err != nil {
		return nil, fmt.Errorf("invalid %s value: %w", f.typ, err)
	}
	return v, nil
}

func (f *Field[T]) checkAny(vc *validation, path string, v any) {
	if tv, ok := v.(T); ok {
		f.check(vc, path, tv)
	}
}

func (f *Field[T]) getterAny() func() (any, error) {
	if f.getter == nil {
		return nil
	}
	g := f.getter
	return func() (any, error) { return g() }
}

func (f *Field[T]) watch(path string, out *[]WatchEntry) {
	if !f.isLive {
		return
	}
	w := WatchEntry{Key: path, Type: f.typ, Env: f.env}
	if f.getter != nil {
		g := f.getter
		w.Getter = func() (any, error) { return g() }
	}
	if f.onChange != nil {
		fn := f.onChange
		w.OnChange = func(old, new any) {
			o, oerr := convert.To[T](old)
			n, nerr := convert.To[T](new)
			if oerr != nil || nerr != nil {
				return
			}
			fn(o, n)
		}
	}
	*out = append(*out, w)
}

// ---------------------------------------------------------------------------
// List
// ---------------------------------------------------------------------------

// ListEntry is a list config entry whose elements are described by an element entry.
type ListEntry struct {
	base
	elem       Entry
	hasDefault bool
	def        any
	validators []func([]any) error
	onChange   func(old, new []any)
	getter     func() ([]any, error)
}

// List declares a list config entry. elemType describes each element, e.g.
// List("hosts", String("host")). Element validators run on every element.
//
// Limits: only scalar element types (String, Int, Bool, Float64, Duration) are
// converted and validated per element; Object, List and Computed elements are
// passed through unchecked. Only the element entry's Validate functions are
// used — other options set on it (Required, Env, Flag, Getter, IsLive,
// DependsOn, and so on) are ignored during validation.
func List(id string, elemType Entry) *ListEntry {
	if elemType == nil {
		panic(fmt.Sprintf("cfgx: List %q: element type must not be nil", id))
	}
	return &ListEntry{base: newBase(id), elem: elemType}
}

// Flag sets the cobra long flag (e.g. "--hosts") and an optional short flag.
func (l *ListEntry) Flag(long string, short ...string) *ListEntry {
	l.setFlag(long, short)
	return l
}

// Env sets the environment variable bound to this entry.
func (l *ListEntry) Env(name string) *ListEntry {
	l.env = name
	return l
}

// Default sets the default value. val must be a slice or array; otherwise Default panics.
func (l *ListEntry) Default(val any) *ListEntry {
	if val != nil {
		k := reflect.TypeOf(val).Kind()
		if k != reflect.Slice && k != reflect.Array {
			panic(fmt.Sprintf("cfgx: List %q: Default requires a slice or array, got %T", l.entryID, val))
		}
	}
	l.hasDefault = true
	l.def = val
	return l
}

// Required marks the entry as required: loading fails if it is unset or empty.
func (l *ListEntry) Required() *ListEntry {
	l.required = true
	return l
}

// Sensitive marks the entry as a secret that must be redacted in output.
func (l *ListEntry) Sensitive() *ListEntry {
	l.sensitive = true
	return l
}

// Desc sets the human-readable description.
func (l *ListEntry) Desc(text string) *ListEntry {
	l.desc = text
	return l
}

// Validate appends a validator for the whole list. Validators run in declaration order.
func (l *ListEntry) Validate(fn func([]any) error) *ListEntry {
	if fn == nil {
		panic(fmt.Sprintf("cfgx: entry %q: Validate called with nil function", l.entryID))
	}
	l.validators = append(l.validators, fn)
	return l
}

// IsLive marks the entry as hot-reloadable.
func (l *ListEntry) IsLive() *ListEntry {
	l.isLive = true
	return l
}

// OnChange sets the callback invoked when a live value changes.
func (l *ListEntry) OnChange(fn func(old, new []any)) *ListEntry {
	l.onChange = fn
	return l
}

// Getter sets a lazy-load function used instead of viper to obtain the value.
func (l *ListEntry) Getter(fn func() ([]any, error)) *ListEntry {
	l.getter = fn
	return l
}

// Deprecated marks the entry as deprecated with the given message.
func (l *ListEntry) Deprecated(message string) *ListEntry {
	l.deprecated = message
	return l
}

// Scope restricts the entry to the given cobra commands.
func (l *ListEntry) Scope(commands ...string) *ListEntry {
	l.scope = copyStrings(commands)
	return l
}

// Tags sets documentation grouping labels.
func (l *ListEntry) Tags(tags ...string) *ListEntry {
	l.tags = copyStrings(tags)
	return l
}

// DependsOn activates validation of this entry only when the entry id equals value.
func (l *ListEntry) DependsOn(id string, value any) *ListEntry {
	l.dependsOn = &ir.DependsOnIR{ID: id, Value: value}
	return l
}

func (l *ListEntry) toIR() ir.EntryIR {
	e := l.baseIR(ir.TypeList)
	if l.hasDefault && l.def != nil {
		// Normalize each element (e.g. time.Duration -> "1s") per invariant 7.
		rv := reflect.ValueOf(l.def)
		def := make([]any, rv.Len())
		for i := range def {
			def[i] = scalarDefaultIR(rv.Index(i).Interface())
		}
		e.Default = def
	}
	elem := l.elem.toIR()
	e.ElementType = &elem
	e.HasValidate = len(l.validators) > 0
	e.HasOnChange = l.onChange != nil
	e.HasGetter = l.getter != nil
	return e
}

func (l *ListEntry) value(vc *validation, path string) (val []any, set bool, err error) {
	if l.getter != nil {
		v, gerr := l.getter()
		if gerr != nil {
			return nil, false, fmt.Errorf("getter failed: %w", gerr)
		}
		return v, true, nil
	}
	if !vc.src.IsSet(path) {
		return nil, false, nil
	}
	raw, cerr := convert.ToList(vc.src.Get(path))
	if cerr != nil {
		return nil, false, fmt.Errorf("invalid list value: %w", cerr)
	}
	return raw, true, nil
}

func (l *ListEntry) validate(vc *validation, path string) {
	if !vc.active(l.dependsOn, path) {
		return
	}
	val, set, err := l.value(vc, path)
	if err != nil {
		vc.add(path, err.Error())
		return
	}
	if l.required && len(val) == 0 {
		vc.add(path, "required")
		return
	}
	if !set {
		return
	}
	ec, typed := l.elem.(elementChecker)
	if typed {
		conv := make([]any, len(val))
		ok := true
		for i, raw := range val {
			v, cerr := ec.convertAny(raw)
			if cerr != nil {
				msg := cerr.Error()
				if l.sensitive {
					// Do not echo the source value of a secret.
					msg = "invalid element value " + redacted
				}
				vc.add(fmt.Sprintf("%s[%d]", path, i), msg)
				ok = false
				continue
			}
			conv[i] = v
		}
		if !ok {
			return
		}
		val = conv
	}
	start := len(vc.errs)
	for _, fn := range l.validators {
		if err := fn(val); err != nil {
			vc.add(path, err.Error())
		}
	}
	if l.sensitive {
		vc.redactFrom(start, val...)
	}
	if typed {
		for i, v := range val {
			start = len(vc.errs)
			ec.checkAny(vc, fmt.Sprintf("%s[%d]", path, i), v)
			if l.sensitive {
				vc.redactFrom(start, v)
			}
		}
	}
}

func (l *ListEntry) getterAny() func() (any, error) {
	if l.getter == nil {
		return nil
	}
	g := l.getter
	return func() (any, error) { return g() }
}

func (l *ListEntry) watch(path string, out *[]WatchEntry) {
	if !l.isLive {
		return
	}
	w := WatchEntry{Key: path, Type: ir.TypeList, Env: l.env}
	if l.getter != nil {
		g := l.getter
		w.Getter = func() (any, error) { return g() }
	}
	if l.onChange != nil {
		fn := l.onChange
		w.OnChange = func(old, new any) {
			o, oerr := convert.ToList(old)
			n, nerr := convert.ToList(new)
			if oerr != nil || nerr != nil {
				return
			}
			fn(o, n)
		}
	}
	*out = append(*out, w)
}

// ---------------------------------------------------------------------------
// Object
// ---------------------------------------------------------------------------

// ObjectEntry is a nested group of entries. It has no default or validators of
// its own; its properties carry those.
type ObjectEntry struct {
	base
	properties []Entry
}

// Object declares a nested config object with the given properties.
func Object(id string, properties ...Entry) *ObjectEntry {
	return &ObjectEntry{base: newBase(id), properties: append([]Entry(nil), properties...)}
}

// Desc sets the human-readable description.
func (o *ObjectEntry) Desc(text string) *ObjectEntry {
	o.desc = text
	return o
}

// Deprecated marks the object as deprecated with the given message.
func (o *ObjectEntry) Deprecated(message string) *ObjectEntry {
	o.deprecated = message
	return o
}

// Scope restricts the object to the given cobra commands.
func (o *ObjectEntry) Scope(commands ...string) *ObjectEntry {
	o.scope = copyStrings(commands)
	return o
}

// Tags sets documentation grouping labels.
func (o *ObjectEntry) Tags(tags ...string) *ObjectEntry {
	o.tags = copyStrings(tags)
	return o
}

// DependsOn activates validation of the whole object only when the entry id equals value.
func (o *ObjectEntry) DependsOn(id string, value any) *ObjectEntry {
	o.dependsOn = &ir.DependsOnIR{ID: id, Value: value}
	return o
}

func (o *ObjectEntry) toIR() ir.EntryIR {
	e := o.baseIR(ir.TypeObject)
	for _, p := range o.properties {
		e.Properties = append(e.Properties, p.toIR())
	}
	return e
}

func (o *ObjectEntry) validate(vc *validation, path string) {
	if !vc.active(o.dependsOn, path) {
		return
	}
	for _, p := range o.properties {
		pp := joinPath(path, p.id())
		if vc.hidden[pp] {
			continue
		}
		p.validate(vc, pp)
	}
}

func (o *ObjectEntry) watch(path string, out *[]WatchEntry) {
	for _, p := range o.properties {
		p.watch(joinPath(path, p.id()), out)
	}
}

// ---------------------------------------------------------------------------
// Computed
// ---------------------------------------------------------------------------

// ComputedEntry is a derived value computed from its parent config. It has no
// flag, env or default.
type ComputedEntry[T any] struct {
	base
	fn       func(parent any) T
	onChange func(old, new T)
}

// Computed declares a derived entry whose value is fn applied to the parent config.
func Computed[T any](id string, fn func(parent any) T) *ComputedEntry[T] {
	if fn == nil {
		panic(fmt.Sprintf("cfgx: Computed %q: fn must not be nil", id))
	}
	return &ComputedEntry[T]{base: newBase(id), fn: fn}
}

// Desc sets the human-readable description.
func (c *ComputedEntry[T]) Desc(text string) *ComputedEntry[T] {
	c.desc = text
	return c
}

// Tags sets documentation grouping labels.
func (c *ComputedEntry[T]) Tags(tags ...string) *ComputedEntry[T] {
	c.tags = copyStrings(tags)
	return c
}

// Scope restricts the entry to the given cobra commands.
func (c *ComputedEntry[T]) Scope(commands ...string) *ComputedEntry[T] {
	c.scope = copyStrings(commands)
	return c
}

// IsLive marks the entry as recomputed on every access.
func (c *ComputedEntry[T]) IsLive() *ComputedEntry[T] {
	c.isLive = true
	return c
}

// OnChange sets the callback invoked when the computed value changes.
func (c *ComputedEntry[T]) OnChange(fn func(old, new T)) *ComputedEntry[T] {
	c.onChange = fn
	return c
}

func (c *ComputedEntry[T]) toIR() ir.EntryIR {
	e := c.baseIR(ir.TypeComputed)
	e.HasGetter = true
	e.HasOnChange = c.onChange != nil
	e.ResultType = resultTypeName[T]()
	return e
}

// resultTypeName renders T as Go source for EntryIR.ResultType: predeclared
// and time types by name, composites structurally, and "any" for the empty
// interface.
func resultTypeName[T any]() string {
	t := reflect.TypeOf((*T)(nil)).Elem()
	if t.Kind() == reflect.Interface && t.NumMethod() == 0 && t.PkgPath() == "" {
		return "any"
	}
	return t.String()
}

func (c *ComputedEntry[T]) computeAny(parent any) any { return c.fn(parent) }

func (c *ComputedEntry[T]) validate(*validation, string) {}

func (c *ComputedEntry[T]) watch(path string, out *[]WatchEntry) {
	if !c.isLive {
		return
	}
	fn := c.fn
	w := WatchEntry{
		Key:     path,
		Type:    ir.TypeComputed,
		Compute: func(parent any) any { return fn(parent) },
	}
	if c.onChange != nil {
		cb := c.onChange
		w.OnChange = func(old, new any) {
			o, ook := asComputed[T](old)
			n, nok := asComputed[T](new)
			if !ook || !nok {
				return
			}
			cb(o, n)
		}
	}
	*out = append(*out, w)
}

// asComputed asserts v to T, treating nil as the zero value of T.
func asComputed[T any](v any) (T, bool) {
	if v == nil {
		var zero T
		return zero, true
	}
	t, ok := v.(T)
	return t, ok
}
