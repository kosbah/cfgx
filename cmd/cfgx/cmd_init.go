package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/epistemer/cfgx/cmd/cfgx/scaffold"
)

type initOptions struct {
	dir     string
	force   bool
	noTidy  bool
	replace string
}

func newInitCmd() *cobra.Command {
	var o initOptions
	cmd := &cobra.Command{
		Use:   "init <module-path>",
		Short: "Scaffold a new cobra/viper+cfgx project",
		Long: `init creates a new project for the given module path: go.mod, main.go,
cmd/root.go, cmd/serve.go, cmd/migrate.go, an example cmd/schema.go and an empty
pkg/ directory. It then generates the config code and artifacts from the schema
and runs go mod tidy.

Existing files are skipped unless --force is given.`,
		Example: "  cfgx init github.com/yourorg/myapp",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cmd.OutOrStdout(), cmd.ErrOrStderr(), args[0], o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.dir, "dir", "", "target directory (default: last element of module path)")
	f.BoolVar(&o.force, "force", false, "overwrite existing files")
	f.BoolVar(&o.noTidy, "no-tidy", false, "skip running go mod tidy after scaffolding")
	f.StringVar(&o.replace, "cfgx-replace", "", "add a go.mod replace directive pointing github.com/epistemer/cfgx at this directory")
	if err := f.MarkHidden("cfgx-replace"); err != nil {
		panic(err) // the flag is defined just above
	}
	return cmd
}

func runInit(stdout, stderr io.Writer, modulePath string, o initOptions) error {
	modulePath = strings.TrimSuffix(strings.TrimSpace(modulePath), "/")
	if modulePath == "" || strings.ContainsAny(modulePath, " \t\n\\") {
		return fmt.Errorf("invalid module path %q", modulePath)
	}
	appName := path.Base(modulePath)
	dir := o.dir
	if dir == "" {
		dir = appName
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	data := scaffold.ScaffoldData{
		ModulePath:  modulePath,
		AppName:     appName,
		GoVersion:   goVersion(),
		Year:        time.Now().Year(),
		CfgxVersion: cfgxModuleVersion(),
	}
	if o.replace != "" {
		abs, err := filepath.Abs(o.replace)
		if err != nil {
			return err
		}
		data.CfgxReplace = modFilePath(abs)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, f := range scaffold.Files {
		target := filepath.Join(dir, filepath.FromSlash(f.Path))
		if _, err := os.Stat(target); err == nil && !o.force {
			fmt.Fprintf(stdout, "⚠ skipped %s (already exists; use --force to overwrite)\n", f.Path)
			continue
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		content, err := scaffold.Render(f, data)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, content, 0o644); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "✓ created %s\n", f.Path)
	}
	for _, d := range scaffold.Dirs {
		if err := os.MkdirAll(filepath.Join(dir, filepath.FromSlash(d)), 0o755); err != nil {
			return err
		}
	}

	// Generate config.gen.go and the artifacts so the project compiles.
	schema := filepath.Join(dir, filepath.FromSlash(scaffold.SchemaPath))
	if err := runGen(stdout, stderr, genOptions{schema: schema}); err != nil {
		return err
	}

	if !o.noTidy {
		tidy := exec.Command("go", "mod", "tidy")
		tidy.Dir = dir
		tidy.Stdout = stderr
		tidy.Stderr = stderr
		if err := tidy.Run(); err != nil {
			return fmt.Errorf("go mod tidy in %s failed: %w (fix the problem and rerun it, or use --no-tidy)", dir, err)
		}
		fmt.Fprintln(stdout, "✓ ran go mod tidy")
	}

	cwd, err := os.Getwd()
	if err != nil {
		cwd = "" // displayPath then falls back to the absolute path
	}
	fmt.Fprintf(stdout, `
Created %s in %s.

Next steps:
  cd %s
  go run . serve --help       # see the flags generated from cmd/schema.go
  $EDITOR cmd/schema.go       # declare your configuration
  go generate ./cmd/...       # regenerate config code, examples and docs
`, modulePath, displayPath(cwd, dir), displayPath(cwd, dir))
	return nil
}

// goVersion returns the major.minor version of the running Go toolchain for
// the scaffolded go.mod, or "1.21" if it cannot be determined.
func goVersion() string {
	v := strings.TrimPrefix(goruntime.Version(), "go")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 || parts[0] != "1" {
		return "1.21"
	}
	minor := parts[1]
	if i := strings.IndexFunc(minor, func(r rune) bool { return r < '0' || r > '9' }); i >= 0 {
		minor = minor[:i]
	}
	if n, err := strconv.Atoi(minor); err != nil || n < 21 {
		return "1.21"
	}
	return "1." + minor
}

// cfgxModuleVersion is the cfgx version required by scaffolded projects: the
// version of this binary if it is a release, else v0.0.0 (use --cfgx-replace).
func cfgxModuleVersion() string {
	return moduleVersion(cfgxVersion())
}

// moduleVersion returns v if it can appear in a go.mod require directive, else
// v0.0.0. Go 1.24+ stamps binaries built inside a checkout with a VCS
// pseudo-version, which carries "+dirty" when the tree has local changes; build
// metadata is not allowed in go.mod.
func moduleVersion(v string) string {
	if strings.HasPrefix(v, "v") && !strings.Contains(v, "+") {
		return v
	}
	return "v0.0.0"
}

// modFilePath formats a directory for a go.mod replace directive, quoting it
// if it contains characters that need it.
func modFilePath(p string) string {
	p = filepath.ToSlash(p)
	if strings.ContainsAny(p, " \t\"'`") {
		return strconv.Quote(p)
	}
	return p
}
