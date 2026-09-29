package parser

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	goparser "go/parser"
	"go/printer"
	"go/scanner"
	"go/token"
	"math/big"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/example/cfgx/internal/ir"
)

// cfgxImportPath is the import path of the cfgx DSL package.
const cfgxImportPath = "github.com/example/cfgx"

// ParseError describes a structured parse failure at a position in the schema file.
type ParseError struct {
	File    string
	Line    int
	Column  int
	Message string
}

// Error formats the failure as "file:line:col: message". When no position is
// known (for example, the file could not be read), it is "file: message".
func (e *ParseError) Error() string {
	return formatDiag(e.File, e.Line, e.Column, e.Message)
}

// Warning describes a non-fatal problem found while parsing, such as an entry
// that is both Required and has a Default (SchemaIR invariant 9).
type Warning struct {
	File    string
	Line    int
	Column  int
	Message string
}

// String formats the warning like ParseError: "file:line:col: message".
func (w Warning) String() string {
	return formatDiag(w.File, w.Line, w.Column, w.Message)
}

func formatDiag(file string, line, col int, msg string) string {
	if line <= 0 {
		return fmt.Sprintf("%s: %s", file, msg)
	}
	return fmt.Sprintf("%s:%d:%d: %s", file, line, col, msg)
}

// Parse reads the Go source file at filename, locates the cfgx.Define(...)
// call, and returns its SchemaIR representation.
//
// Parse performs static analysis only — it does not compile or run the source.
// All literal values are extracted. Function literals are detected but not
// extracted; their presence is noted via HasOnChange / HasGetter / HasValidate flags.
//
// Parse returns a *ParseError for any structural problem. All SchemaIR invariants
// from docs/design/schema-ir-spec.md are validated before returning. Warnings
// are discarded; use ParseWithWarnings to obtain them.
func Parse(filename string) (*ir.SchemaIR, error) {
	s, _, err := ParseWithWarnings(filename)
	return s, err
}

// ParseWithWarnings is like Parse but also returns the non-fatal warnings found
// in the schema, in source order. Currently the only warning is SchemaIR
// invariant 9: an entry that is Required and also has a Default.
func ParseWithWarnings(filename string) (*ir.SchemaIR, []Warning, error) {
	src, err := os.ReadFile(filename)
	if err != nil {
		return nil, nil, &ParseError{File: filename, Message: err.Error()}
	}
	fset := token.NewFileSet()
	file, err := goparser.ParseFile(fset, filename, src, goparser.SkipObjectResolution)
	if err != nil {
		var list scanner.ErrorList
		if errors.As(err, &list) && len(list) > 0 {
			pos := list[0].Pos
			return nil, nil, &ParseError{File: filename, Line: pos.Line, Column: pos.Column, Message: list[0].Msg}
		}
		return nil, nil, &ParseError{File: filename, Message: err.Error()}
	}
	p := &schemaParser{fset: fset, file: filename}
	schema, err := p.parseFile(file)
	if err != nil {
		return nil, nil, err
	}
	return schema, p.warnings, nil
}

// schemaParser holds the state of a single Parse run.
type schemaParser struct {
	fset     *token.FileSet
	file     string
	pkg      string // local name of the cfgx import
	timePkg  string // local name of the time import; "" if not imported
	warnings []Warning
}

// entryNode is an entry under construction together with the source positions
// needed for post-parse checks. Children are linked here and folded into the
// IR by toIR.
type entryNode struct {
	ir         ir.EntryIR
	pos        token.Pos
	dependsArg ast.Expr // the DependsOn id argument, if any
	props      []*entryNode
	elem       *entryNode
}

func (n *entryNode) toIR() ir.EntryIR {
	e := n.ir
	for _, c := range n.props {
		e.Properties = append(e.Properties, c.toIR())
	}
	if n.elem != nil {
		el := n.elem.toIR()
		e.ElementType = &el
	}
	return e
}

// constraintNode is a parsed constraint with the positions of its ID arguments.
type constraintNode struct {
	ir   ir.ConstraintIR
	args []ast.Expr
}

// chainCall is one call in a builder chain: the constructor or a method.
type chainCall struct {
	name *ast.Ident
	call *ast.CallExpr
}

func (p *schemaParser) errAt(node ast.Node, format string, args ...any) *ParseError {
	return p.errAtPos(node.Pos(), format, args...)
}

func (p *schemaParser) errAtPos(pos token.Pos, format string, args ...any) *ParseError {
	position := p.fset.Position(pos)
	return &ParseError{File: p.file, Line: position.Line, Column: position.Column, Message: fmt.Sprintf(format, args...)}
}

func (p *schemaParser) warnAt(pos token.Pos, format string, args ...any) {
	position := p.fset.Position(pos)
	p.warnings = append(p.warnings, Warning{File: p.file, Line: position.Line, Column: position.Column, Message: fmt.Sprintf(format, args...)})
}

// render returns a short source rendering of expr for error messages.
func (p *schemaParser) render(expr ast.Expr) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, p.fset, expr); err != nil {
		return fmt.Sprintf("%T", expr)
	}
	s := strings.Join(strings.Fields(buf.String()), " ")
	if r := []rune(s); len(r) > 40 {
		s = string(r[:37]) + "..."
	}
	return s
}

func unparen(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			return e
		}
		e = p.X
	}
}

func isNilIdent(e ast.Expr) bool {
	id, ok := unparen(e).(*ast.Ident)
	return ok && id.Name == "nil"
}

// ---------------------------------------------------------------------------
// File level
// ---------------------------------------------------------------------------

func (p *schemaParser) parseFile(f *ast.File) (*ir.SchemaIR, error) {
	if err := p.resolveImports(f); err != nil {
		return nil, err
	}
	define, err := p.findDefine(f)
	if err != nil {
		return nil, err
	}
	if define.Ellipsis.IsValid() {
		return nil, p.errAtPos(define.Ellipsis, "%s.Define arguments must be listed individually, not spread with ...", p.pkg)
	}

	var roots []*entryNode
	var constraints []constraintNode
	for _, arg := range define.Args {
		ctor, methods, err := p.peel(arg)
		if err != nil {
			return nil, err
		}
		if ctor.name.Name == "MutuallyExclusive" {
			c, err := p.parseConstraint(ctor, methods)
			if err != nil {
				return nil, err
			}
			constraints = append(constraints, c)
			continue
		}
		n, err := p.buildEntry(arg, ctor, methods)
		if err != nil {
			return nil, err
		}
		roots = append(roots, n)
	}
	if err := p.checkUnique(roots); err != nil {
		return nil, err
	}

	index := map[string]*entryNode{}
	indexNodes(index, roots, "")
	if err := p.checkDependsOn(index, roots, ""); err != nil {
		return nil, err
	}
	for _, c := range constraints {
		for i, id := range c.ir.EntryIDs {
			if index[id] == nil {
				return nil, p.errAt(c.args[i], "%s references unknown entry ID %q", "MutuallyExclusive", id)
			}
		}
	}

	out := &ir.SchemaIR{Version: ir.Version, Entries: make([]ir.EntryIR, 0, len(roots))}
	for _, n := range roots {
		out.Entries = append(out.Entries, n.toIR())
	}
	for _, c := range constraints {
		out.Constraints = append(out.Constraints, c.ir)
	}
	return out, nil
}

// resolveImports records the local names of the cfgx and time imports.
func (p *schemaParser) resolveImports(f *ast.File) error {
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return p.errAt(imp.Path, "invalid import path %s", imp.Path.Value)
		}
		switch path {
		case cfgxImportPath:
			name := "cfgx"
			if imp.Name != nil {
				name = imp.Name.Name
			}
			switch name {
			case "_":
				// A blank import binds no name; a named import may still exist.
			case ".":
				return p.errAt(imp, "%q must be imported by name, not with %q", cfgxImportPath, name)
			default:
				p.pkg = name
			}
		case "time":
			if imp.Name == nil {
				p.timePkg = "time"
			} else if n := imp.Name.Name; n != "." && n != "_" {
				p.timePkg = n
			}
		}
	}
	if p.pkg == "" {
		return p.errAt(f.Name, "file does not import %q by name", cfgxImportPath)
	}
	return nil
}

// findDefine returns the single <pkg>.Define(...) call in the file.
func (p *schemaParser) findDefine(f *ast.File) (*ast.CallExpr, error) {
	var calls []*ast.CallExpr
	ast.Inspect(f, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if name, ok := p.pkgSelector(call.Fun); ok && name == "Define" {
				calls = append(calls, call)
			}
		}
		return true
	})
	switch len(calls) {
	case 0:
		return nil, p.errAt(f.Name, "no %s.Define(...) call found", p.pkg)
	case 1:
		return calls[0], nil
	default:
		first := p.fset.Position(calls[0].Pos())
		return nil, p.errAt(calls[1], "more than one %s.Define(...) call; a schema file must contain exactly one (first at line %d)", p.pkg, first.Line)
	}
}

// pkgSelector reports whether expr is <pkg>.Name and returns Name.
func (p *schemaParser) pkgSelector(expr ast.Expr) (string, bool) {
	sel, ok := unparen(expr).(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	id, ok := unparen(sel.X).(*ast.Ident)
	if !ok || id.Name != p.pkg {
		return "", false
	}
	return sel.Sel.Name, true
}

// peel walks a builder chain from the outermost call down to the constructor.
// Methods are returned innermost-first, i.e. in the order they execute.
func (p *schemaParser) peel(expr ast.Expr) (chainCall, []chainCall, error) {
	var methods []chainCall
	for {
		call, ok := unparen(expr).(*ast.CallExpr)
		if !ok {
			return chainCall{}, nil, p.errAt(expr, "expected a %s entry constructor call, got %s", p.pkg, p.render(expr))
		}
		fun := unparen(call.Fun)
		typeArgs := false
		switch f := fun.(type) {
		case *ast.IndexExpr:
			fun, typeArgs = unparen(f.X), true
		case *ast.IndexListExpr:
			fun, typeArgs = unparen(f.X), true
		}
		sel, ok := fun.(*ast.SelectorExpr)
		if !ok {
			return chainCall{}, nil, p.errAt(call.Fun, "expected a %s entry constructor call, got %s", p.pkg, p.render(call.Fun))
		}
		if id, ok := unparen(sel.X).(*ast.Ident); ok {
			if id.Name != p.pkg {
				return chainCall{}, nil, p.errAt(id, "entries must be built inline from %s constructors, got %s", p.pkg, p.render(sel.X))
			}
			if typeArgs && sel.Sel.Name != "Computed" {
				return chainCall{}, nil, p.errAt(call.Fun, "%s.%s does not take type arguments", p.pkg, sel.Sel.Name)
			}
			for i, j := 0, len(methods)-1; i < j; i, j = i+1, j-1 {
				methods[i], methods[j] = methods[j], methods[i]
			}
			return chainCall{name: sel.Sel, call: call}, methods, nil
		}
		if typeArgs {
			return chainCall{}, nil, p.errAt(call.Fun, "method %s does not take type arguments", sel.Sel.Name)
		}
		methods = append(methods, chainCall{name: sel.Sel, call: call})
		expr = sel.X
	}
}

// ---------------------------------------------------------------------------
// Entries
// ---------------------------------------------------------------------------

// scalarCtors maps the scalar constructors to their entry types.
var scalarCtors = map[string]ir.EntryType{
	"String":   ir.TypeString,
	"Int":      ir.TypeInt,
	"Bool":     ir.TypeBool,
	"Float64":  ir.TypeFloat64,
	"Duration": ir.TypeDuration,
}

// fieldMethods is the method set of scalar and List entries.
var fieldMethods = setOf("Flag", "Env", "Default", "Required", "Sensitive", "Desc", "Validate",
	"IsLive", "OnChange", "Getter", "Deprecated", "Scope", "Tags", "DependsOn")

// methodSets lists the builder methods available on each entry type.
var methodSets = map[ir.EntryType]map[string]bool{
	ir.TypeString:   fieldMethods,
	ir.TypeInt:      fieldMethods,
	ir.TypeBool:     fieldMethods,
	ir.TypeFloat64:  fieldMethods,
	ir.TypeDuration: fieldMethods,
	ir.TypeList:     fieldMethods,
	ir.TypeObject:   setOf("Desc", "Deprecated", "Scope", "Tags", "DependsOn"),
	ir.TypeComputed: setOf("Desc", "Tags", "Scope", "IsLive", "OnChange"),
}

func setOf(names ...string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// parseEntry parses a nested entry expression (an Object property or a List element).
func (p *schemaParser) parseEntry(expr ast.Expr) (*entryNode, error) {
	ctor, methods, err := p.peel(expr)
	if err != nil {
		return nil, err
	}
	return p.buildEntry(expr, ctor, methods)
}

func (p *schemaParser) buildEntry(expr ast.Expr, ctor chainCall, methods []chainCall) (*entryNode, error) {
	name := ctor.name.Name
	call := ctor.call
	args := call.Args
	n := &entryNode{pos: expr.Pos()}
	e := &n.ir

	if call.Ellipsis.IsValid() {
		return nil, p.errAtPos(call.Ellipsis, "spread arguments (...) are not supported in %s.%s", p.pkg, name)
	}
	wantArgs := func(want int) error {
		if len(args) != want {
			return p.errAt(call, "%s.%s expects %d argument(s), got %d", p.pkg, name, want, len(args))
		}
		return nil
	}

	if t, ok := scalarCtors[name]; ok {
		e.Type = t
		if err := wantArgs(1); err != nil {
			return nil, err
		}
	} else {
		switch name {
		case "List":
			e.Type = ir.TypeList
			if err := wantArgs(2); err != nil {
				return nil, err
			}
		case "Object":
			e.Type = ir.TypeObject
			if len(args) < 2 {
				// Invariant 2: an Object must have at least one property.
				return nil, p.errAt(call, "%s.Object requires an ID and at least one property", p.pkg)
			}
		case "Computed":
			e.Type = ir.TypeComputed
			if err := wantArgs(2); err != nil {
				return nil, err
			}
		case "MutuallyExclusive":
			return nil, p.errAt(call, "%s.MutuallyExclusive is a schema constraint and must be passed directly to %s.Define", p.pkg, p.pkg)
		default:
			return nil, p.errAt(ctor.name, "unknown %s constructor %q", p.pkg, name)
		}
	}

	id, err := p.stringLit(args[0], "entry ID")
	if err != nil {
		return nil, err
	}
	if id == "" {
		return nil, p.errAt(args[0], "entry ID must not be empty")
	}
	if strings.ContainsAny(id, ".[]") {
		return nil, p.errAt(args[0], "entry ID %q must not contain '.', '[' or ']'; use Object for nesting", id)
	}
	e.ID = id

	switch e.Type {
	case ir.TypeList:
		elem, err := p.parseEntry(args[1])
		if err != nil {
			return nil, err
		}
		n.elem = elem
	case ir.TypeObject:
		for _, a := range args[1:] {
			c, err := p.parseEntry(a)
			if err != nil {
				return nil, err
			}
			n.props = append(n.props, c)
		}
		if err := p.checkUnique(n.props); err != nil {
			return nil, err
		}
	case ir.TypeComputed:
		if isNilIdent(args[1]) {
			return nil, p.errAt(args[1], "%s.Computed %q: fn must not be nil", p.pkg, id)
		}
		e.HasGetter = true
	}

	allowed := methodSets[e.Type]
	for _, m := range methods {
		if !allowed[m.name.Name] {
			return nil, p.errAt(m.name, "unknown method %q on %s entry %q", m.name.Name, e.Type, id)
		}
		if err := p.applyMethod(n, m); err != nil {
			return nil, err
		}
	}

	// Invariant 8: a sensitive entry must be settable.
	if e.Sensitive && !e.HasGetter && e.Env == "" {
		return nil, p.errAtPos(n.pos, "sensitive entry %q must have an Env or a Getter; otherwise it can never be set", id)
	}
	// Invariant 9: Required with a Default is only a warning.
	if e.Required && e.Default != nil {
		p.warnAt(n.pos, "entry %q is Required but also has a Default, so Required can never fail", id)
	}
	return n, nil
}

// applyMethod applies one builder method to n, mirroring the runtime builder.
func (p *schemaParser) applyMethod(n *entryNode, m chainCall) error {
	e := &n.ir
	name := m.name.Name
	args := m.call.Args
	if m.call.Ellipsis.IsValid() && name != "Scope" && name != "Tags" {
		return p.errAtPos(m.call.Ellipsis, "spread arguments (...) are not supported in %s", name)
	}
	wantArgs := func(want int) error {
		if len(args) != want {
			return p.errAt(m.call, "%s expects %d argument(s), got %d", name, want, len(args))
		}
		return nil
	}

	var err error
	switch name {
	case "Flag":
		if len(args) < 1 || len(args) > 2 {
			return p.errAt(m.call, "Flag expects a long flag and at most one short flag, got %d argument(s)", len(args))
		}
		if e.FlagLong, err = p.stringLit(args[0], "Flag argument"); err != nil {
			return err
		}
		e.FlagShort = ""
		if len(args) == 2 {
			if e.FlagShort, err = p.stringLit(args[1], "Flag argument"); err != nil {
				return err
			}
		}
	case "Env":
		if err = wantArgs(1); err == nil {
			e.Env, err = p.stringLit(args[0], "Env argument")
		}
	case "Desc":
		if err = wantArgs(1); err == nil {
			e.Description, err = p.stringLit(args[0], "Desc argument")
		}
	case "Deprecated":
		if err = wantArgs(1); err == nil {
			e.Deprecated, err = p.stringLit(args[0], "Deprecated argument")
		}
	case "Required":
		if err = wantArgs(0); err == nil {
			e.Required = true
		}
	case "Sensitive":
		if err = wantArgs(0); err == nil {
			e.Sensitive = true
		}
	case "IsLive":
		if err = wantArgs(0); err == nil {
			e.IsLive = true
		}
	case "Scope", "Tags":
		var vals []string
		if vals, _, err = p.stringArgs(m.call, name+" argument"); err == nil {
			if len(vals) == 0 {
				vals = nil
			}
			if name == "Scope" {
				e.Scope = vals
			} else {
				e.Tags = vals
			}
		}
	case "Default":
		if err = wantArgs(1); err == nil {
			var elemType ir.EntryType
			if n.elem != nil {
				elemType = n.elem.ir.Type
			}
			e.Default, err = p.defaultValue(e.Type, elemType, args[0])
		}
	case "DependsOn":
		if err = wantArgs(2); err != nil {
			return err
		}
		id, err := p.stringLit(args[0], "DependsOn ID")
		if err != nil {
			return err
		}
		val, err := p.evalValue(args[1], "DependsOn value")
		if err != nil {
			return err
		}
		e.DependsOn = &ir.DependsOnIR{ID: id, Value: val}
		n.dependsArg = args[0]
	case "Validate":
		if err = wantArgs(1); err == nil {
			err = p.applyValidate(n, args[0])
		}
	case "OnChange":
		if err = wantArgs(1); err == nil {
			e.HasOnChange = !isNilIdent(args[0])
		}
	case "Getter":
		if err = wantArgs(1); err == nil {
			e.HasGetter = !isNilIdent(args[0])
		}
	default:
		// Unreachable: callers check the method set first.
		err = p.errAt(m.name, "unknown method %q", name)
	}
	return err
}

// checkUnique enforces invariant 1 within one scope.
func (p *schemaParser) checkUnique(nodes []*entryNode) error {
	seen := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		if seen[n.ir.ID] {
			return p.errAtPos(n.pos, "duplicate entry ID %q", n.ir.ID)
		}
		seen[n.ir.ID] = true
	}
	return nil
}

// indexNodes maps every addressable entry (root and Object properties) by its dot path.
func indexNodes(index map[string]*entryNode, nodes []*entryNode, parent string) {
	for _, n := range nodes {
		path := joinPath(parent, n.ir.ID)
		index[path] = n
		indexNodes(index, n.props, path)
	}
}

// checkDependsOn enforces invariant 5. A DependsOn ID resolves as a sibling
// first, then as a dot path from the schema root, as at runtime. A List
// element resolves relative to the List's scope.
func (p *schemaParser) checkDependsOn(index map[string]*entryNode, nodes []*entryNode, parent string) error {
	for _, n := range nodes {
		if err := p.checkNodeDependsOn(index, n, parent, nil); err != nil {
			return err
		}
		if err := p.checkDependsOn(index, n.props, joinPath(parent, n.ir.ID)); err != nil {
			return err
		}
	}
	return nil
}

// checkNodeDependsOn checks n's DependsOn, resolving it in local (element
// siblings, may be nil), then as a sibling under parent, then from the root.
func (p *schemaParser) checkNodeDependsOn(index map[string]*entryNode, n *entryNode, parent string, local map[string]bool) error {
	if dep := n.ir.DependsOn; dep != nil {
		found := local[dep.ID] || index[dep.ID] != nil
		if parent != "" && index[parent+"."+dep.ID] != nil {
			found = true
		}
		if !found {
			return p.errAt(n.dependsArg, "DependsOn references unknown entry ID %q", dep.ID)
		}
	}
	if n.elem != nil {
		if err := p.checkNodeDependsOn(index, n.elem, parent, nil); err != nil {
			return err
		}
		return p.checkElemDependsOn(index, n.elem.props, parent)
	}
	return nil
}

// checkElemDependsOn checks the properties of an Object used as a List
// element. They are not addressable by path, so a DependsOn ID resolves as a
// sibling within the same Object first, then as for the List itself.
func (p *schemaParser) checkElemDependsOn(index map[string]*entryNode, nodes []*entryNode, parent string) error {
	local := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		local[n.ir.ID] = true
	}
	for _, n := range nodes {
		if err := p.checkNodeDependsOn(index, n, parent, local); err != nil {
			return err
		}
		if err := p.checkElemDependsOn(index, n.props, parent); err != nil {
			return err
		}
	}
	return nil
}

func joinPath(parent, id string) string {
	if parent == "" {
		return id
	}
	return parent + "." + id
}

// ---------------------------------------------------------------------------
// Constraints
// ---------------------------------------------------------------------------

func (p *schemaParser) parseConstraint(ctor chainCall, methods []chainCall) (constraintNode, error) {
	if len(methods) > 0 {
		return constraintNode{}, p.errAt(methods[0].name, "unknown method %q on %s.MutuallyExclusive constraint", methods[0].name.Name, p.pkg)
	}
	ids, exprs, err := p.stringArgs(ctor.call, "MutuallyExclusive ID")
	if err != nil {
		return constraintNode{}, err
	}
	if len(ids) < 2 {
		return constraintNode{}, p.errAt(ctor.call, "%s.MutuallyExclusive requires at least two IDs, got %d", p.pkg, len(ids))
	}
	return constraintNode{
		ir:   ir.ConstraintIR{Type: ir.ConstraintMutuallyExclusive, EntryIDs: ids},
		args: exprs,
	}, nil
}

// ---------------------------------------------------------------------------
// Validators
// ---------------------------------------------------------------------------

// builtinSpec describes a built-in validator: its IR name and the entry type it validates.
type builtinSpec struct {
	name      string
	valueType ir.EntryType
}

// builtins maps the cfgx validator constructors to their IR names
// (docs/design/schema-ir-spec.md, "Built-in validator names and params").
var builtins = map[string]builtinSpec{
	"Email":             {"email", ir.TypeString},
	"URL":               {"url", ir.TypeString},
	"Port":              {"port", ir.TypeInt},
	"PortRange":         {"port_range", ir.TypeInt},
	"Base64":            {"base64", ir.TypeString},
	"Regex":             {"regex", ir.TypeString},
	"DurationValidator": {"duration", ir.TypeString},
	"Semver":            {"semver", ir.TypeString},
	"Enum":              {"enum", ir.TypeString},
	"Range":             {"range", ir.TypeFloat64},
}

// applyValidate records a Validate argument: a call to a cfgx built-in becomes a
// ValidatorIR; anything else (function literal, identifier, other call) is a
// custom validator and sets HasValidate.
func (p *schemaParser) applyValidate(n *entryNode, arg ast.Expr) error {
	if isNilIdent(arg) {
		return p.errAt(arg, "Validate called with nil function")
	}
	if call, ok := unparen(arg).(*ast.CallExpr); ok {
		if name, ok := p.pkgSelector(call.Fun); ok {
			v, err := p.builtinValidator(n.ir.Type, name, call)
			if err != nil {
				return err
			}
			n.ir.BuiltinValidators = append(n.ir.BuiltinValidators, v)
			return nil
		}
	}
	if name, ok := p.pkgSelector(arg); ok {
		return p.errAt(arg, "%s.%s is not a validator; built-in validators must be called, e.g. %s.%s()", p.pkg, name, p.pkg, name)
	}
	n.ir.HasValidate = true
	return nil
}

func (p *schemaParser) builtinValidator(t ir.EntryType, name string, call *ast.CallExpr) (ir.ValidatorIR, error) {
	spec, ok := builtins[name]
	if !ok {
		return ir.ValidatorIR{}, p.errAt(call.Fun, "%s.%s is not a built-in validator", p.pkg, name)
	}
	if spec.valueType != t {
		return ir.ValidatorIR{}, p.errAt(call, "%s.%s validates %s values and cannot be used on a %s entry", p.pkg, name, spec.valueType, t)
	}
	if call.Ellipsis.IsValid() && name != "Enum" {
		return ir.ValidatorIR{}, p.errAtPos(call.Ellipsis, "spread arguments (...) are not supported in %s.%s", p.pkg, name)
	}
	args := call.Args
	wantArgs := func(want int) error {
		if len(args) != want {
			return p.errAt(call, "%s.%s expects %d argument(s), got %d", p.pkg, name, want, len(args))
		}
		return nil
	}
	v := ir.ValidatorIR{Name: spec.name}
	switch name {
	case "PortRange":
		if err := wantArgs(2); err != nil {
			return v, err
		}
		lo, err := p.intLit(args[0], "PortRange min")
		if err != nil {
			return v, err
		}
		hi, err := p.intLit(args[1], "PortRange max")
		if err != nil {
			return v, err
		}
		v.Params = map[string]any{"min": lo, "max": hi}
	case "Range":
		if err := wantArgs(2); err != nil {
			return v, err
		}
		lo, err := p.floatLit(args[0], "Range min")
		if err != nil {
			return v, err
		}
		hi, err := p.floatLit(args[1], "Range max")
		if err != nil {
			return v, err
		}
		v.Params = map[string]any{"min": lo, "max": hi}
	case "Regex":
		if err := wantArgs(1); err != nil {
			return v, err
		}
		pattern, err := p.stringLit(args[0], "Regex pattern")
		if err != nil {
			return v, err
		}
		if _, err := regexp.Compile(pattern); err != nil {
			return v, p.errAt(args[0], "invalid Regex pattern: %v", err)
		}
		v.Params = map[string]any{"pattern": pattern}
	case "Enum":
		vals, _, err := p.stringArgs(call, "Enum value")
		if err != nil {
			return v, err
		}
		if vals == nil {
			vals = []string{}
		}
		v.Params = map[string]any{"values": vals}
	default:
		if err := wantArgs(0); err != nil {
			return v, err
		}
	}
	return v, nil
}

// ---------------------------------------------------------------------------
// Literals
// ---------------------------------------------------------------------------

// stringLit evaluates a string literal (or a + concatenation of string literals).
func (p *schemaParser) stringLit(expr ast.Expr, what string) (string, error) {
	switch x := unparen(expr).(type) {
	case *ast.BasicLit:
		if x.Kind == token.STRING {
			s, err := strconv.Unquote(x.Value)
			if err != nil {
				return "", p.errAt(x, "invalid string literal %s", x.Value)
			}
			return s, nil
		}
	case *ast.BinaryExpr:
		if x.Op == token.ADD {
			l, err := p.stringLit(x.X, what)
			if err != nil {
				return "", err
			}
			r, err := p.stringLit(x.Y, what)
			if err != nil {
				return "", err
			}
			return l + r, nil
		}
	}
	return "", p.errAt(expr, "%s must be a literal string, got %s", what, p.render(expr))
}

// stringArgs evaluates the (variadic) string arguments of call, expanding a
// trailing []string{...}... composite literal.
func (p *schemaParser) stringArgs(call *ast.CallExpr, what string) ([]string, []ast.Expr, error) {
	exprs := call.Args
	if call.Ellipsis.IsValid() && len(exprs) > 0 {
		last := exprs[len(exprs)-1]
		cl, ok := unparen(last).(*ast.CompositeLit)
		if !ok {
			return nil, nil, p.errAt(last, "%s must be a literal, got %s", what, p.render(last))
		}
		exprs = append(append([]ast.Expr(nil), exprs[:len(exprs)-1]...), cl.Elts...)
	}
	var vals []string
	for _, a := range exprs {
		s, err := p.stringLit(a, what)
		if err != nil {
			return nil, nil, err
		}
		vals = append(vals, s)
	}
	return vals, exprs, nil
}

func (p *schemaParser) intLit(expr ast.Expr, what string) (int, error) {
	v, err := p.evalValue(expr, what)
	if err != nil {
		return 0, err
	}
	if n, ok := asInt(v); ok {
		return n, nil
	}
	return 0, p.errAt(expr, "%s must be an integer literal, got %s", what, p.render(expr))
}

func (p *schemaParser) floatLit(expr ast.Expr, what string) (float64, error) {
	v, err := p.evalValue(expr, what)
	if err != nil {
		return 0, err
	}
	switch n := v.(type) {
	case int:
		return float64(n), nil
	case float64:
		return n, nil
	}
	return 0, p.errAt(expr, "%s must be a number literal, got %s", what, p.render(expr))
}

// asInt converts an evaluated literal to int if it is an integer value.
func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		if n == float64(int(n)) {
			return int(n), true
		}
	}
	return 0, false
}

// listElemKinds maps the List element entry types that accept a Default to
// the matching slice element kind (see elemKind).
var listElemKinds = map[ir.EntryType]string{
	ir.TypeString:   "string",
	ir.TypeInt:      "int",
	ir.TypeBool:     "bool",
	ir.TypeFloat64:  "float64",
	ir.TypeDuration: "duration",
}

// defaultValue evaluates a Default argument for an entry of type t (invariant 7).
// For a List, elemType is the type of its element entry.
func (p *schemaParser) defaultValue(t, elemType ir.EntryType, arg ast.Expr) (any, error) {
	switch t {
	case ir.TypeDuration:
		d, err := p.evalDuration(arg, "Default")
		if err != nil {
			return nil, err
		}
		return d.String(), nil
	case ir.TypeList:
		if isNilIdent(arg) {
			return nil, nil
		}
		cl, ok := unparen(arg).(*ast.CompositeLit)
		if !ok {
			return nil, p.errAt(arg, "Default of a list entry must be a slice literal, got %s", p.render(arg))
		}
		want, ok := listElemKinds[elemType]
		if !ok {
			return nil, p.errAt(arg, "Default is not supported for a list of %s elements", elemType)
		}
		return p.evalList(cl, "Default element", want)
	}
	v, err := p.evalValue(arg, "Default")
	if err != nil {
		return nil, err
	}
	switch t {
	case ir.TypeString:
		if _, ok := v.(string); ok {
			return v, nil
		}
	case ir.TypeInt:
		if n, ok := asInt(v); ok {
			return n, nil
		}
	case ir.TypeFloat64:
		switch n := v.(type) {
		case int:
			return float64(n), nil
		case float64:
			return n, nil
		}
	case ir.TypeBool:
		if _, ok := v.(bool); ok {
			return v, nil
		}
	}
	return nil, p.errAt(arg, "Default of a %s entry must be a %s literal, got %s", t, t, p.render(arg))
}

// evalValue evaluates a literal: numbers (int or float64, optionally negated),
// strings, true/false/nil, slice literals of literals, and time.Duration
// expressions (rendered as duration strings).
func (p *schemaParser) evalValue(expr ast.Expr, what string) (any, error) {
	if _, isList := unparen(expr).(*ast.CompositeLit); !isList && p.isDurationExpr(expr) {
		d, err := p.evalDuration(expr, what)
		if err != nil {
			return nil, err
		}
		return d.String(), nil
	}
	switch x := unparen(expr).(type) {
	case *ast.BasicLit:
		switch x.Kind {
		case token.STRING:
			return p.stringLit(x, what)
		case token.INT:
			n, err := strconv.ParseInt(x.Value, 0, strconv.IntSize)
			if err != nil {
				return nil, p.errAt(x, "%s: integer literal %s out of range", what, x.Value)
			}
			return int(n), nil
		case token.FLOAT:
			f, err := strconv.ParseFloat(x.Value, 64)
			if err != nil {
				return nil, p.errAt(x, "%s: invalid float literal %s", what, x.Value)
			}
			return f, nil
		default:
			return nil, p.errAt(x, "%s: %s literals are not supported", what, strings.ToLower(x.Kind.String()))
		}
	case *ast.UnaryExpr:
		if x.Op == token.SUB || x.Op == token.ADD {
			v, err := p.evalValue(x.X, what)
			if err != nil {
				return nil, err
			}
			neg := x.Op == token.SUB
			switch n := v.(type) {
			case int:
				if neg {
					return -n, nil
				}
				return n, nil
			case float64:
				if neg {
					return -n, nil
				}
				return n, nil
			}
			return nil, p.errAt(x, "%s: unary %s requires a number, got %s", what, x.Op, p.render(x.X))
		}
	case *ast.BinaryExpr:
		if x.Op == token.ADD {
			return p.stringLit(x, what)
		}
	case *ast.Ident:
		switch x.Name {
		case "true":
			return true, nil
		case "false":
			return false, nil
		case "nil":
			return nil, nil
		}
	case *ast.CompositeLit:
		return p.evalList(x, what, "")
	}
	return nil, p.errAt(expr, "%s must be a literal, got %s", what, p.render(expr))
}

// evalList evaluates a []T{...} or [N]T{...} composite literal of literals.
// If want is non-empty, the elements must be of that kind (see elemKind): the
// literal's element type must match it, or be any with every element fitting.
func (p *schemaParser) evalList(cl *ast.CompositeLit, what, want string) ([]any, error) {
	at, ok := cl.Type.(*ast.ArrayType)
	if !ok {
		return nil, p.errAt(cl, "%s must be a slice literal, got %s", what, p.render(cl))
	}
	kind, ok := p.elemKind(at.Elt)
	if !ok {
		return nil, p.errAt(at.Elt, "unsupported list element type %s", p.render(at.Elt))
	}
	if want != "" && kind != "any" && kind != want {
		return nil, p.errAt(at.Elt, "%s type %s does not match the list's %s element entry", what, p.render(at.Elt), want)
	}
	out := make([]any, 0, len(cl.Elts))
	for _, el := range cl.Elts {
		if _, ok := el.(*ast.KeyValueExpr); ok {
			return nil, p.errAt(el, "%s: keyed elements are not supported", what)
		}
		var (
			v   any
			err error
		)
		switch kind {
		case "duration":
			var d time.Duration
			if d, err = p.evalDuration(el, what); err == nil {
				v = d.String()
			}
		case "string":
			v, err = p.stringLit(el, what)
		case "int":
			v, err = p.intLit(el, what)
		case "float64":
			v, err = p.floatLit(el, what)
		case "bool":
			v, err = p.evalValue(el, what)
			if _, ok := v.(bool); err == nil && !ok {
				err = p.errAt(el, "%s must be a bool literal, got %s", what, p.render(el))
			}
		default: // any
			v, err = p.evalValue(el, what)
			if err == nil && want != "" && !fitsKind(v, want) {
				err = p.errAt(el, "%s %s does not fit the list's %s element entry", what, p.render(el), want)
			}
		}
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// fitsKind reports whether an evaluated literal is a valid value of kind.
func fitsKind(v any, kind string) bool {
	switch kind {
	case "int":
		_, ok := v.(int)
		return ok
	case "float64":
		switch v.(type) {
		case int, float64:
			return true
		}
	case "string":
		_, ok := v.(string)
		return ok
	case "bool":
		_, ok := v.(bool)
		return ok
	case "duration":
		s, ok := v.(string)
		if ok {
			_, err := time.ParseDuration(s)
			return err == nil
		}
	}
	return false
}

// elemKind classifies a slice element type expression.
func (p *schemaParser) elemKind(t ast.Expr) (string, bool) {
	switch x := unparen(t).(type) {
	case *ast.Ident:
		switch x.Name {
		case "string", "int", "bool", "float64":
			return x.Name, true
		case "any":
			return "any", true
		}
	case *ast.InterfaceType:
		if x.Methods == nil || len(x.Methods.List) == 0 {
			return "any", true
		}
	case *ast.SelectorExpr:
		if id, ok := x.X.(*ast.Ident); ok && p.timePkg != "" && id.Name == p.timePkg && x.Sel.Name == "Duration" {
			return "duration", true
		}
	}
	return "", false
}

// timeUnits maps the time package's duration constants to nanoseconds.
var timeUnits = map[string]time.Duration{
	"Nanosecond":  time.Nanosecond,
	"Microsecond": time.Microsecond,
	"Millisecond": time.Millisecond,
	"Second":      time.Second,
	"Minute":      time.Minute,
	"Hour":        time.Hour,
}

// isDurationExpr reports whether expr refers to the time package.
func (p *schemaParser) isDurationExpr(expr ast.Expr) bool {
	if p.timePkg == "" {
		return false
	}
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == p.timePkg {
				found = true
			}
		}
		return !found
	})
	return found
}

// evalDuration evaluates a constant duration expression such as 30*time.Second,
// time.Minute, time.Second/2, time.Duration(5) or a plain integer (nanoseconds).
func (p *schemaParser) evalDuration(expr ast.Expr, what string) (time.Duration, error) {
	r, _, err := p.evalConst(expr, what)
	if err != nil {
		return 0, err
	}
	if !r.IsInt() || !r.Num().IsInt64() {
		return 0, p.errAt(expr, "%s: %s is not a whole number of nanoseconds that fits a time.Duration", what, p.render(expr))
	}
	return time.Duration(r.Num().Int64()), nil
}

// constKind is the kind of a constant expression, ordered so that the kind of
// a binary expression is the larger of its operands' kinds.
type constKind int

const (
	untypedInt   constKind = iota // e.g. 30
	untypedFloat                  // e.g. 1.5
	typedDur                      // time.Duration, e.g. time.Second
)

// evalConst evaluates a numeric constant expression exactly, following Go's
// constant rules: an untyped operand of a time.Duration expression must be a
// whole number, and division of integers or durations truncates toward zero.
func (p *schemaParser) evalConst(expr ast.Expr, what string) (*big.Rat, constKind, error) {
	switch x := unparen(expr).(type) {
	case *ast.BasicLit:
		if x.Kind == token.INT {
			if n, err := strconv.ParseInt(x.Value, 0, 64); err == nil {
				return new(big.Rat).SetInt64(n), untypedInt, nil
			}
			return nil, 0, p.errAt(x, "%s: unsupported number literal %s", what, x.Value)
		}
		if x.Kind == token.FLOAT {
			if r, ok := new(big.Rat).SetString(strings.ReplaceAll(x.Value, "_", "")); ok {
				return r, untypedFloat, nil
			}
			return nil, 0, p.errAt(x, "%s: unsupported number literal %s", what, x.Value)
		}
	case *ast.SelectorExpr:
		if id, ok := x.X.(*ast.Ident); ok && id.Name == p.timePkg {
			if u, ok := timeUnits[x.Sel.Name]; ok {
				return new(big.Rat).SetInt64(int64(u)), typedDur, nil
			}
		}
	case *ast.UnaryExpr:
		if x.Op == token.SUB || x.Op == token.ADD {
			r, k, err := p.evalConst(x.X, what)
			if err != nil {
				return nil, 0, err
			}
			if x.Op == token.SUB {
				r.Neg(r)
			}
			return r, k, nil
		}
	case *ast.BinaryExpr:
		if x.Op == token.ADD || x.Op == token.SUB || x.Op == token.MUL || x.Op == token.QUO {
			l, lk, err := p.evalConst(x.X, what)
			if err != nil {
				return nil, 0, err
			}
			r, rk, err := p.evalConst(x.Y, what)
			if err != nil {
				return nil, 0, err
			}
			k := max(lk, rk)
			if k == typedDur && (!l.IsInt() || !r.IsInt()) {
				return nil, 0, p.errAt(x, "%s: %s truncates a non-integer constant to time.Duration", what, p.render(x))
			}
			switch x.Op {
			case token.ADD:
				return l.Add(l, r), k, nil
			case token.SUB:
				return l.Sub(l, r), k, nil
			case token.MUL:
				return l.Mul(l, r), k, nil
			default:
				if r.Sign() == 0 {
					return nil, 0, p.errAt(x, "%s: division by zero in %s", what, p.render(x))
				}
				if k == untypedFloat {
					return l.Quo(l, r), k, nil
				}
				q := new(big.Int).Quo(l.Num(), r.Num()) // both integers: truncated division
				return new(big.Rat).SetInt(q), k, nil
			}
		}
	case *ast.CallExpr:
		// time.Duration(x) conversion.
		if sel, ok := unparen(x.Fun).(*ast.SelectorExpr); ok && len(x.Args) == 1 && !x.Ellipsis.IsValid() {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == p.timePkg && sel.Sel.Name == "Duration" {
				r, _, err := p.evalConst(x.Args[0], what)
				return r, typedDur, err
			}
		}
	}
	return nil, 0, p.errAt(expr, "%s must be a literal duration (e.g. 30 * time.Second), got %s", what, p.render(expr))
}
