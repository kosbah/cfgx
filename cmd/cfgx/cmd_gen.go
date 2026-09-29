package main

import (
	"bytes"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/epistemer/cfgx/gen/artifacts"
	"github.com/epistemer/cfgx/gen/codegen"
	schemaparser "github.com/epistemer/cfgx/gen/parser"
	"github.com/epistemer/cfgx/internal/ir"
)

type genOptions struct {
	schema string
	outDir string
	pkg    string
	noEnv  bool
	noYAML bool
	noJSON bool
	noDocs bool
	dryRun bool
}

func newGenCmd() *cobra.Command {
	var o genOptions
	cmd := &cobra.Command{
		Use:   "gen",
		Short: "Generate typed Go code and artifacts from a schema",
		Long: `gen parses the schema file and writes config.gen.go next to it, plus
.env.example, config.example.yaml, config.schema.json, docs/config.md and
docs/config.1 in the output directory (default: the nearest directory at or
above the schema that contains go.mod, else the schema's directory).

Without --schema, gen looks for a single schema.go containing cfgx.Define
under the current directory.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGen(cmd.OutOrStdout(), cmd.ErrOrStderr(), o)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&o.schema, "schema", "s", "", "path to schema Go file (default: auto-discover)")
	f.StringVarP(&o.outDir, "out-dir", "o", "", "output directory for generated artifacts (default: module root)")
	f.StringVarP(&o.pkg, "pkg", "p", "", "Go package name (default: the schema file's package)")
	f.BoolVar(&o.noEnv, "no-env", false, "skip .env.example")
	f.BoolVar(&o.noYAML, "no-yaml", false, "skip config.example.yaml")
	f.BoolVar(&o.noJSON, "no-json-schema", false, "skip config.schema.json")
	f.BoolVar(&o.noDocs, "no-docs", false, "skip docs/config.md and docs/config.1")
	f.BoolVar(&o.dryRun, "dry-run", false, "print what would be generated without writing")
	return cmd
}

func runGen(stdout, stderr io.Writer, o genOptions) error {
	p, err := loadProject(stderr, o.schema, 1)
	if err != nil {
		return err
	}
	pkg := o.pkg
	if pkg == "" {
		if pkg, err = packageName(p.schemaAbs); err != nil {
			return err
		}
	}
	src, err := codegen.Generate(p.ir, codegen.Options{
		Package: pkg,
		Module:  codegen.DefaultModule,
		Source:  p.source(),
	})
	if err != nil {
		return err
	}
	outDir := p.root
	if o.outDir != "" {
		if outDir, err = filepath.Abs(o.outDir); err != nil {
			return err
		}
	}
	files := []outFile{{path: filepath.Join(filepath.Dir(p.schemaAbs), "config.gen.go"), data: src}}
	var kinds []string
	if !o.noEnv {
		kinds = append(kinds, "env")
	}
	if !o.noYAML {
		kinds = append(kinds, "yaml")
	}
	if !o.noJSON {
		kinds = append(kinds, "json-schema")
	}
	if !o.noDocs {
		kinds = append(kinds, "md", "man")
	}
	for _, k := range kinds {
		a := artifactKinds[k]
		data, err := a.gen(p)
		if err != nil {
			return err
		}
		files = append(files, outFile{path: filepath.Join(outDir, filepath.FromSlash(a.genPath)), data: data})
	}
	return writeFiles(stdout, files, o.dryRun)
}

// ---------------------------------------------------------------------------
// Shared pipeline
// ---------------------------------------------------------------------------

// project is a parsed schema together with the locations derived from it.
type project struct {
	schemaAbs string       // absolute path of the schema file
	ir        *ir.SchemaIR // parsed schema
	modRoot   string       // nearest directory at or above the schema with go.mod; "" if none
	root      string       // modRoot, or the schema's directory
}

// source is the schema path relative to the module root, for the generated
// file's "// Source:" line.
func (p *project) source() string {
	if p.modRoot != "" {
		if rel, err := filepath.Rel(p.modRoot, p.schemaAbs); err == nil {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.Base(p.schemaAbs)
}

// appName is the application name used in the man page: the name of the
// module root (or schema) directory.
func (p *project) appName() string {
	return filepath.Base(p.root)
}

// loadProject resolves (or discovers) the schema file and parses it. Errors
// are returned as *exitError with failCode; a parse error is printed as
// "✗ parse error: file:line:col: message".
func loadProject(stderr io.Writer, schema string, failCode int) (*project, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, &exitError{Code: failCode, Err: err}
	}
	if schema == "" {
		if schema, err = discoverSchema(cwd); err != nil {
			return nil, &exitError{Code: failCode, Err: err}
		}
	}
	abs, err := filepath.Abs(schema)
	if err != nil {
		return nil, &exitError{Code: failCode, Err: err}
	}
	if fi, err := os.Stat(abs); err != nil {
		return nil, &exitError{Code: failCode, Err: fmt.Errorf("schema file %s: %w", schema, err)}
	} else if fi.IsDir() {
		return nil, &exitError{Code: failCode, Err: fmt.Errorf("schema file %s is a directory", schema)}
	}
	s, warnings, err := schemaparser.ParseWithWarnings(displayPath(cwd, abs))
	if err != nil {
		fmt.Fprintf(stderr, "✗ parse error: %v\n", err)
		return nil, &exitError{Code: failCode}
	}
	for _, w := range warnings {
		fmt.Fprintf(stderr, "⚠ %s\n", w)
	}
	p := &project{schemaAbs: abs, ir: s, modRoot: findModRoot(filepath.Dir(abs))}
	p.root = p.modRoot
	if p.root == "" {
		p.root = filepath.Dir(abs)
	}
	return p, nil
}

// discoverSchema finds the single schema.go containing cfgx.Define under dir.
// Hidden, underscore-prefixed, vendor, testdata and node_modules directories
// are skipped.
func discoverSchema(dir string) (string, error) {
	var found []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are ignored
		}
		if d.IsDir() {
			name := d.Name()
			if path != dir && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") ||
				name == "vendor" || name == "testdata" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "schema.go" {
			return nil
		}
		if b, err := os.ReadFile(path); err == nil && bytes.Contains(b, []byte("cfgx.Define")) {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("no schema found: no schema.go containing cfgx.Define under %s; use --schema to name one", dir)
	case 1:
		return found[0], nil
	}
	sort.Strings(found)
	var b strings.Builder
	fmt.Fprintf(&b, "found %d schema files; use --schema to pick one:", len(found))
	for _, f := range found {
		b.WriteString("\n  " + displayPath(dir, f))
	}
	return "", errors.New(b.String())
}

// findModRoot returns the nearest directory at or above dir containing go.mod,
// or "" if there is none.
func findModRoot(dir string) string {
	for {
		if fi, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil && !fi.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// packageName returns the package clause of the Go file at path.
func packageName(path string) (string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.PackageClauseOnly)
	if err != nil {
		return "", err
	}
	return f.Name.Name, nil
}

// displayPath returns path relative to cwd when it lies under cwd or inside
// the module containing cwd (so `go generate` in cmd/ prints ../.env.example),
// otherwise the absolute path.
func displayPath(cwd, path string) string {
	rel, err := filepath.Rel(cwd, path)
	if err != nil {
		return path
	}
	if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return rel
	}
	if mod := findModRoot(cwd); mod != "" {
		if r, err := filepath.Rel(mod, path); err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return rel
		}
	}
	return path
}

// artifactKind is one generated artifact.
type artifactKind struct {
	genPath  string // path under the gen output directory (slash-separated)
	docsName string // file name under the docs output directory
	gen      func(p *project) ([]byte, error)
}

// artifactKinds maps the docs --format names (plus env and yaml) to generators.
var artifactKinds = map[string]artifactKind{
	"env": {genPath: ".env.example", gen: func(p *project) ([]byte, error) {
		return artifacts.EnvExample(p.ir)
	}},
	"yaml": {genPath: "config.example.yaml", gen: func(p *project) ([]byte, error) {
		return artifacts.YAMLExample(p.ir)
	}},
	"json-schema": {genPath: "config.schema.json", docsName: "config.schema.json", gen: func(p *project) ([]byte, error) {
		return artifacts.JSONSchema(p.ir)
	}},
	"md": {genPath: "docs/config.md", docsName: "config.md", gen: func(p *project) ([]byte, error) {
		return artifacts.Markdown(p.ir)
	}},
	"man": {genPath: "docs/config.1", docsName: "config.1", gen: func(p *project) ([]byte, error) {
		return artifacts.ManPage(p.ir, p.appName(), cfgxVersion(), time.Now().Format("2006-01-02"))
	}},
}

// outFile is a file to write.
type outFile struct {
	path string // absolute
	data []byte
}

// writeFiles writes files (unless dryRun) and prints one summary line each,
// with paths relative to the current directory.
func writeFiles(stdout io.Writer, files []outFile, dryRun bool) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	names := make([]string, len(files))
	width := 0
	for i, f := range files {
		names[i] = displayPath(cwd, f.path)
		if n := len([]rune(names[i])); n > width {
			width = n
		}
	}
	verb := "✓ wrote"
	if dryRun {
		verb = "would write"
	}
	for i, f := range files {
		if !dryRun {
			if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(f.path, f.data, 0o644); err != nil {
				return err
			}
		}
		pad := strings.Repeat(" ", width-len([]rune(names[i])))
		fmt.Fprintf(stdout, "%s %s%s (%d lines)\n", verb, names[i], pad, countLines(f.data))
	}
	return nil
}

func countLines(b []byte) int {
	n := bytes.Count(b, []byte("\n"))
	if len(b) > 0 && b[len(b)-1] != '\n' {
		n++
	}
	return n
}
