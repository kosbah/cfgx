package codegen

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"go/format"
	"go/token"
	"math"
	"path"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/example/cfgx/internal/ir"
)

// DefaultModule is the cfgx module path used when Options.Module is empty.
const DefaultModule = "github.com/example/cfgx"

// Options configures code generation.
type Options struct {
	// Package is the Go package name for the generated file (e.g. "cmd").
	Package string

	// Module is the module import path of cfgx (e.g. "github.com/example/cfgx").
	// It is used to build the import paths of cfgx and cfgx/runtime. Empty
	// means DefaultModule.
	Module string

	// Source is the schema file path shown in the "// Source:" header line.
	// The line is omitted if Source is empty.
	Source string
}

//go:embed templates/config.go.tmpl
var templateFS embed.FS

var tmpl = template.Must(template.New("config.go.tmpl").Funcs(template.FuncMap{
	"quote":   strconv.Quote,
	"lit":     literal,
	"zero":    zeroLiteral,
	"comment": comment,
}).ParseFS(templateFS, "templates/config.go.tmpl"))

// Generate produces a formatted Go source file from schema.
// The returned bytes are ready to write to disk; they are gofmt-clean.
func Generate(schema *ir.SchemaIR, opts Options) ([]byte, error) {
	if schema == nil {
		return nil, errors.New("codegen: nil schema")
	}
	if schema.Version != ir.Version {
		return nil, fmt.Errorf("codegen: unsupported SchemaIR version %q (want %q)", schema.Version, ir.Version)
	}
	if !isIdent(opts.Package) {
		return nil, fmt.Errorf("codegen: invalid package name %q", opts.Package)
	}
	if opts.Module == "" {
		opts.Module = DefaultModule
	}
	b := &builder{opts: opts, flagNames: map[string]string{}, flagShorts: map[string]string{}}
	f, err := b.build(schema)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, f); err != nil {
		return nil, fmt.Errorf("codegen: executing template: %w", err)
	}
	out, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("codegen: formatting generated source: %w", err)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// View model
// ---------------------------------------------------------------------------

type fieldKind int

const (
	kindValue fieldKind = iota
	kindObject
	kindComputed
)

// fileDef is the data passed to the template.
type fileDef struct {
	Package    string
	Source     string
	CfgxAlias  string // non-empty if the cfgx package name differs from "cfgx"
	CfgxPath   string
	Runtime    string
	StdImports []string

	Root    *structDef
	Scopes  []*structDef
	Objects []*structDef
	Flags   []*fieldDef
	Binds   []*fieldDef // entries with Env or Default
	Loads   []*loadDef

	HasSensitive     bool
	HasGetter        bool // any non-computed entry with a Getter
	HasComputed      bool
	HasWatch         bool // any Load starts a watcher
	HasWatchValue    bool
	HasWatchComputed bool
}

// NeedsAs reports whether the cfgxAs helper is used.
func (f *fileDef) NeedsAs() bool { return f.HasGetter || f.HasWatchValue }

// structDef is a generated struct: the root Config, a scope struct or an Object struct.
type structDef struct {
	Name     string
	Scope    string // scope structs only
	Key      string // Object structs only: the object's viper key
	Desc     string
	Embed    bool   // scope structs embed Config
	Root     bool   // the root or a scope struct
	Watcher  bool   // the root struct when a watcher is used: it gets a watcher field
	LoadFunc string // scope structs only: the name of their Load function
	Fields   []*fieldDef
}

// fieldDef is one entry as a struct field plus its getter method.
type fieldDef struct {
	ID         string
	Key        string // viper key
	Field      string // unexported field name
	Method     string // exported getter name
	Kind       fieldKind
	GoType     string // getter result type (value and computed entries)
	Object     *structDef
	ViperGet   string // viper method reading the value, e.g. "GetInt"
	FlagFunc   string // pflag method defining the flag, e.g. "IntP"
	FlagName   string
	FlagShort  string
	Env        string
	Default    any
	Desc       string
	Deprecated string
	Sensitive  bool // stored as sensitiveString
	Redact     bool // flag default is not shown
	Live       bool
	LivePtr    bool // live and sensitive: stored in a *atomic.Value
	Getter     bool
	OnChange   bool
}

// Storage returns the Go type of the struct field.
func (f *fieldDef) Storage() string {
	switch {
	case f.Kind == kindObject:
		return f.Object.Name
	case f.Kind == kindComputed:
		return "func() " + f.GoType
	case f.LivePtr:
		return "*atomic.Value"
	case f.Live:
		return "atomic.Value"
	case f.Sensitive:
		return "sensitiveString"
	}
	return f.GoType
}

// HasDefault reports whether the entry has a default value.
func (f *fieldDef) HasDefault() bool { return f.Default != nil }

// IsObject, IsComputed report the entry kind to the template.
func (f *fieldDef) IsObject() bool   { return f.Kind == kindObject }
func (f *fieldDef) IsComputed() bool { return f.Kind == kindComputed }

// loadDef is one Load function.
type loadDef struct {
	Func          string
	Type          string
	Scope         string
	Values        []*loadItem
	Computed      []*loadItem
	Watches       []*loadItem
	WatchComputed []*loadItem
}

// HasWatch reports whether the Load function starts a watcher.
func (l *loadDef) HasWatch() bool { return len(l.Watches)+len(l.WatchComputed) > 0 }

// loadItem is a field reached from the Load function's cfg variable.
type loadItem struct {
	Path   []string // field path from cfg
	Parent []string // computed only: path of the containing struct; empty means cfg
	F      *fieldDef
}

// ---------------------------------------------------------------------------
// Building
// ---------------------------------------------------------------------------

type builder struct {
	opts       Options
	file       *fileDef
	objects    []*structDef
	objPaths   []string
	flagNames  map[string]string // flag name -> key
	flagShorts map[string]string
}

// rootReserved are method and field names used by the generated root and
// scope structs themselves.
var rootReserved = map[string]string{
	"Config":      "the embedded Config field of scope structs",
	"Close":       "the Close method",
	"cfgxWatcher": "the watcher field",
}

func (b *builder) build(s *ir.SchemaIR) (*fileDef, error) {
	f := &fileDef{Package: b.opts.Package, Source: b.opts.Source, CfgxPath: b.opts.Module, Runtime: b.opts.Module + "/runtime"}
	if base := path.Base(b.opts.Module); base != "cfgx" {
		f.CfgxAlias = "cfgx"
	}
	b.file = f
	f.Root = &structDef{Name: "Config", Root: true}

	fields, err := b.fields(s.Entries, "", "the schema root", true)
	if err != nil {
		return nil, err
	}
	scopes := map[string]*structDef{}
	for i, e := range s.Entries {
		fd := fields[i]
		if len(e.Scope) == 0 {
			f.Root.Fields = append(f.Root.Fields, fd)
			continue
		}
		seen := map[string]bool{}
		for _, sc := range e.Scope {
			if seen[sc] {
				continue
			}
			seen[sc] = true
			sd := scopes[sc]
			if sd == nil {
				name := ToGoName(sc)
				if !isExportedIdent(name) {
					return nil, fmt.Errorf("codegen: scope %q of entry %q does not map to a Go identifier", sc, e.ID)
				}
				sd = &structDef{Name: name + "Config", Scope: sc, Embed: true, Root: true}
				scopes[sc] = sd
				f.Scopes = append(f.Scopes, sd)
			}
			sd.Fields = append(sd.Fields, fd)
		}
	}
	if err := b.nameStructs(); err != nil {
		return nil, err
	}
	f.Objects = b.objects

	f.Loads = append(f.Loads, b.load("Load", f.Root, ""))
	for _, sd := range f.Scopes {
		sd.LoadFunc = "Load" + strings.TrimSuffix(sd.Name, "Config")
		l := b.load(sd.LoadFunc, f.Root, sd.Scope)
		l.Type = sd.Name
		b.collect(l, sd.Fields, nil, nil)
		f.Loads = append(f.Loads, l)
	}
	for _, l := range f.Loads {
		f.HasWatchValue = f.HasWatchValue || len(l.Watches) > 0
		f.HasWatchComputed = f.HasWatchComputed || len(l.WatchComputed) > 0
	}
	f.HasWatch = f.HasWatchValue || f.HasWatchComputed
	f.Root.Watcher = f.HasWatch

	std := map[string]bool{}
	if f.HasWatch {
		std["context"] = true
		std["sync/atomic"] = true
	}
	if f.NeedsAs() {
		std["fmt"] = true
	}
	if b.usesTime(f.Root) || b.anyUsesTime(f.Scopes) || b.anyUsesTime(f.Objects) {
		std["time"] = true
	}
	for p := range std {
		f.StdImports = append(f.StdImports, p)
	}
	sort.Strings(f.StdImports)
	return f, nil
}

func (b *builder) load(fn string, root *structDef, scope string) *loadDef {
	l := &loadDef{Func: fn, Type: root.Name, Scope: scope}
	b.collect(l, root.Fields, nil, nil)
	return l
}

// collect adds the fields reachable from fields to l. prefix is the field
// path of the containing struct; parent is the path of the struct passed to
// Compute for computed fields (nil for the Load function's cfg).
func (b *builder) collect(l *loadDef, fields []*fieldDef, prefix, parent []string) {
	for _, fd := range fields {
		p := append(append([]string(nil), prefix...), fd.Field)
		switch fd.Kind {
		case kindObject:
			b.collect(l, fd.Object.Fields, p, p)
		case kindComputed:
			it := &loadItem{Path: p, Parent: parent, F: fd}
			l.Computed = append(l.Computed, it)
			if fd.Live && fd.OnChange {
				l.WatchComputed = append(l.WatchComputed, it)
			}
		default:
			it := &loadItem{Path: p, F: fd}
			l.Values = append(l.Values, it)
			if fd.Live {
				l.Watches = append(l.Watches, it)
			}
		}
	}
}

// fields converts the entries of one level (the root or an Object) to fields,
// rejecting Go name clashes between siblings.
func (b *builder) fields(entries []ir.EntryIR, parentKey, where string, root bool) ([]*fieldDef, error) {
	methods := map[string]string{}
	names := map[string]string{}
	out := make([]*fieldDef, 0, len(entries))
	for i := range entries {
		e := &entries[i]
		method := ToGoName(e.ID)
		field := toFieldName(e.ID)
		if !isExportedIdent(method) || !isIdent(field) {
			return nil, fmt.Errorf("codegen: entry %q in %s does not map to a Go identifier (got %q)", e.ID, where, method)
		}
		if prev, ok := methods[method]; ok {
			return nil, fmt.Errorf("codegen: entries %q and %q in %s both map to the Go name %s", prev, e.ID, where, method)
		}
		if prev, ok := names[field]; ok {
			return nil, fmt.Errorf("codegen: entries %q and %q in %s both map to the Go field %s", prev, e.ID, where, field)
		}
		if root {
			for _, n := range []string{method, field} {
				if what, ok := rootReserved[n]; ok {
					return nil, fmt.Errorf("codegen: entry %q maps to the Go name %s, which is reserved for %s", e.ID, n, what)
				}
			}
		}
		methods[method], names[field] = e.ID, e.ID
		fd, err := b.field(e, parentKey, method, field)
		if err != nil {
			return nil, err
		}
		out = append(out, fd)
	}
	return out, nil
}

// scalarTypes maps value entry types to their Go type, viper getter and pflag function.
var scalarTypes = map[ir.EntryType][3]string{
	ir.TypeString:   {"string", "GetString", "StringP"},
	ir.TypeInt:      {"int", "GetInt", "IntP"},
	ir.TypeBool:     {"bool", "GetBool", "BoolP"},
	ir.TypeFloat64:  {"float64", "GetFloat64", "Float64P"},
	ir.TypeDuration: {"time.Duration", "GetDuration", "DurationP"},
}

// listTypes maps supported list element types likewise.
var listTypes = map[ir.EntryType][3]string{
	ir.TypeString: {"[]string", "GetStringSlice", "StringSliceP"},
	ir.TypeInt:    {"[]int", "GetIntSlice", "IntSliceP"},
}

func (b *builder) field(e *ir.EntryIR, parentKey, method, field string) (*fieldDef, error) {
	fd := &fieldDef{
		ID:         e.ID,
		Key:        ToViperKey(parentKey, e.ID),
		Field:      field,
		Method:     method,
		Env:        e.Env,
		Default:    e.Default,
		Desc:       e.Description,
		Deprecated: e.Deprecated,
		Live:       e.IsLive,
		Getter:     e.HasGetter,
		OnChange:   e.HasOnChange,
	}
	switch e.Type {
	case ir.TypeObject:
		fd.Kind = kindObject
		fd.Live, fd.Getter, fd.OnChange = false, false, false
		sd := &structDef{Key: fd.Key, Desc: e.Description}
		fd.Object = sd
		b.objects = append(b.objects, sd) // parents before children
		b.objPaths = append(b.objPaths, fd.Key)
		props, err := b.fields(e.Properties, fd.Key, fmt.Sprintf("object %q", fd.Key), false)
		if err != nil {
			return nil, err
		}
		sd.Fields = props
		return fd, nil
	case ir.TypeComputed:
		fd.Kind = kindComputed
		fd.Getter = false
		fd.GoType = resultType(e.ResultType)
		b.file.HasComputed = true
		return fd, nil
	case ir.TypeList:
		if e.ElementType == nil {
			return nil, fmt.Errorf("codegen: list entry %q has no element type", fd.Key)
		}
		t, ok := listTypes[e.ElementType.Type]
		if !ok {
			return nil, fmt.Errorf("codegen: list entry %q: element type %s is not supported (only string and int)", fd.Key, e.ElementType.Type)
		}
		fd.GoType, fd.ViperGet, fd.FlagFunc = t[0], t[1], t[2]
	default:
		t, ok := scalarTypes[e.Type]
		if !ok {
			return nil, fmt.Errorf("codegen: entry %q has unsupported type %q", fd.Key, e.Type)
		}
		fd.GoType, fd.ViperGet, fd.FlagFunc = t[0], t[1], t[2]
	}
	fd.Redact = e.Sensitive
	if e.Sensitive && e.Type == ir.TypeString {
		if e.IsLive {
			fd.LivePtr = true
		} else {
			fd.Sensitive = true
			b.file.HasSensitive = true
		}
	}
	if fd.Getter {
		b.file.HasGetter = true
	}
	if e.FlagLong != "" {
		if err := b.flag(fd, e); err != nil {
			return nil, err
		}
	}
	if fd.Env != "" || fd.Default != nil {
		b.file.Binds = append(b.file.Binds, fd)
	}
	return fd, nil
}

func (b *builder) flag(fd *fieldDef, e *ir.EntryIR) error {
	name := strings.TrimPrefix(e.FlagLong, "--")
	if name == "" || strings.HasPrefix(name, "-") {
		return fmt.Errorf("codegen: entry %q: invalid flag %q", fd.Key, e.FlagLong)
	}
	short := strings.TrimPrefix(e.FlagShort, "-")
	if e.FlagShort != "" && len(short) != 1 {
		return fmt.Errorf("codegen: entry %q: invalid short flag %q (must be one ASCII character)", fd.Key, e.FlagShort)
	}
	if prev, ok := b.flagNames[name]; ok {
		return fmt.Errorf("codegen: entries %q and %q both use the flag --%s", prev, fd.Key, name)
	}
	if prev, ok := b.flagShorts[short]; ok && short != "" {
		return fmt.Errorf("codegen: entries %q and %q both use the short flag -%s", prev, fd.Key, short)
	}
	b.flagNames[name] = fd.Key
	if short != "" {
		b.flagShorts[short] = fd.Key
	}
	fd.FlagName, fd.FlagShort = name, short
	b.file.Flags = append(b.file.Flags, fd)
	return nil
}

// nameStructs names the Object structs: ToStructName of the object ID, or of
// its full path when the short name is shared by several objects or would
// clash with the root or a scope struct.
func (b *builder) nameStructs() error {
	taken := map[string]string{"Config": "the root Config struct"}
	for _, sd := range b.file.Scopes {
		if prev, ok := taken[sd.Name]; ok {
			return fmt.Errorf("codegen: scope %q maps to the struct name %s, already used by %s", sd.Scope, sd.Name, prev)
		}
		taken[sd.Name] = fmt.Sprintf("scope %q", sd.Scope)
	}
	short := make([]string, len(b.objects))
	count := map[string]int{}
	for i, p := range b.objPaths {
		id := p[strings.LastIndex(p, ".")+1:]
		short[i] = ToStructName(id)
		count[short[i]]++
	}
	for i, sd := range b.objects {
		name := short[i]
		if _, clash := taken[name]; clash || count[name] > 1 {
			name = ToStructName(b.objPaths[i])
		}
		if !isExportedIdent(name) {
			return fmt.Errorf("codegen: object %q does not map to a Go identifier (got %q)", b.objPaths[i], name)
		}
		if prev, ok := taken[name]; ok {
			return fmt.Errorf("codegen: object %q maps to the struct name %s, already used by %s", b.objPaths[i], name, prev)
		}
		taken[name] = fmt.Sprintf("object %q", b.objPaths[i])
		sd.Name = name
	}
	return nil
}

func (b *builder) usesTime(sd *structDef) bool {
	for _, fd := range sd.Fields {
		if fd.Kind != kindObject && strings.Contains(fd.GoType, "time.") {
			return true
		}
	}
	return false
}

func (b *builder) anyUsesTime(sds []*structDef) bool {
	for _, sd := range sds {
		if b.usesTime(sd) {
			return true
		}
	}
	return false
}

// basicTypes are the result types a computed getter can declare without imports.
var basicTypes = map[string]bool{
	"string": true, "bool": true, "int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true, "uintptr": true,
	"float32": true, "float64": true, "complex64": true, "complex128": true, "byte": true, "rune": true,
	"error": true, "any": true, "time.Duration": true,
}

// resultType maps an IR ResultType to the computed getter's result type,
// falling back to any for empty or unsupported types.
func resultType(t string) string {
	elem := strings.TrimPrefix(t, "[]")
	if basicTypes[elem] {
		return t
	}
	return "any"
}

// isIdent reports whether s is a Go identifier that is not a keyword.
func isIdent(s string) bool { return token.IsIdentifier(s) }

// ---------------------------------------------------------------------------
// Template functions
// ---------------------------------------------------------------------------

// comment flattens text for use in a // comment.
func comment(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// zeroLiteral renders the zero value of a Go type.
func zeroLiteral(goType string) string {
	switch goType {
	case "string":
		return `""`
	case "bool":
		return "false"
	case "int", "float64", "time.Duration":
		return "0"
	}
	return "nil"
}

// literal renders the IR default v as a Go literal of goType.
func literal(goType string, v any) (string, error) {
	if v == nil {
		return zeroLiteral(goType), nil
	}
	switch goType {
	case "string":
		if s, ok := v.(string); ok {
			return strconv.Quote(s), nil
		}
	case "bool":
		if x, ok := v.(bool); ok {
			return strconv.FormatBool(x), nil
		}
	case "int":
		if n, ok := asInt(v); ok {
			return strconv.FormatInt(n, 10), nil
		}
	case "float64":
		if x, ok := asFloat(v); ok && !math.IsInf(x, 0) && !math.IsNaN(x) {
			s := strconv.FormatFloat(x, 'g', -1, 64)
			if !strings.ContainsAny(s, ".e") {
				s += ".0"
			}
			return s, nil
		}
	case "time.Duration":
		if d, ok := asDuration(v); ok {
			return durationLiteral(d), nil
		}
	case "[]string", "[]int":
		items, ok := v.([]any)
		if !ok {
			break
		}
		elemType := strings.TrimPrefix(goType, "[]")
		elems := make([]string, len(items))
		for i, item := range items {
			s, err := literal(elemType, item)
			if err != nil || item == nil {
				return "", fmt.Errorf("codegen: invalid %s default element %#v", goType, item)
			}
			elems[i] = s
		}
		return goType + "{" + strings.Join(elems, ", ") + "}", nil
	}
	return "", fmt.Errorf("codegen: invalid %s default %#v", goType, v)
}

func asInt(v any) (int64, bool) {
	switch n := v.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		if n == math.Trunc(n) && math.Abs(n) < 1<<63 {
			return int64(n), true
		}
	}
	return 0, false
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

func asDuration(v any) (time.Duration, bool) {
	if s, ok := v.(string); ok {
		d, err := time.ParseDuration(s)
		return d, err == nil
	}
	if n, ok := asInt(v); ok {
		return time.Duration(n), true
	}
	return 0, false
}

// durationUnits are tried from the largest down when rendering a duration.
var durationUnits = []struct {
	d    time.Duration
	name string
}{
	{time.Hour, "time.Hour"},
	{time.Minute, "time.Minute"},
	{time.Second, "time.Second"},
	{time.Millisecond, "time.Millisecond"},
	{time.Microsecond, "time.Microsecond"},
}

// durationLiteral renders d as a constant expression such as 30 * time.Second.
func durationLiteral(d time.Duration) string {
	if d == 0 {
		return "0"
	}
	for _, u := range durationUnits {
		if d%u.d == 0 {
			n := int64(d / u.d)
			if n == 1 {
				return u.name
			}
			return strconv.FormatInt(n, 10) + " * " + u.name
		}
	}
	return "time.Duration(" + strconv.FormatInt(int64(d), 10) + ")"
}
