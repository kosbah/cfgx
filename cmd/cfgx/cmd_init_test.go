package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var scaffoldFiles = []string{"go.mod", "main.go", "cmd/root.go", "cmd/serve.go", "cmd/migrate.go", "cmd/schema.go"}

func TestInit_CreatesProject(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "myapp")
	repo := repoRoot(t)
	out, errOut, code := runCLI(t, "init", "--dir", dir, "--no-tidy", "--cfgx-replace", repo, "github.com/test/myapp")
	if code != 0 {
		t.Fatalf("init: code %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	for _, f := range scaffoldFiles {
		assertExists(t, filepath.Join(dir, filepath.FromSlash(f)))
		assertContains(t, "stdout", out, "✓ created "+f+"\n")
	}
	assertExists(t, filepath.Join(dir, "pkg"), filepath.Join(dir, "cmd", "config.gen.go"),
		filepath.Join(dir, ".env.example"), filepath.Join(dir, "config.example.yaml"),
		filepath.Join(dir, "config.schema.json"), filepath.Join(dir, "docs", "config.md"),
		filepath.Join(dir, "docs", "config.1"))
	if entries, err := os.ReadDir(filepath.Join(dir, "pkg")); err != nil || len(entries) != 0 {
		t.Errorf("pkg/ should be an empty directory: %v %v", entries, err)
	}

	gomod := readFile(t, filepath.Join(dir, "go.mod"))
	assertContains(t, "go.mod", gomod,
		"module github.com/test/myapp\n",
		"github.com/epistemer/cfgx v0.0.0",
		"replace github.com/epistemer/cfgx => "+filepath.ToSlash(repo))
	assertContains(t, "main.go", readFile(t, filepath.Join(dir, "main.go")), `"github.com/test/myapp/cmd"`)
	assertContains(t, "cmd/root.go", readFile(t, filepath.Join(dir, "cmd", "root.go")),
		"RegisterFlags(root, Schema)", "cobra.OnInitialize(", "BindViper(viper.GetViper(), Schema)",
		"// Business logic packages define their own Config interfaces")
	assertContains(t, "cmd/serve.go", readFile(t, filepath.Join(dir, "cmd", "serve.go")),
		"LoadServe(Schema, viper.GetViper())")
	assertContains(t, "cmd/schema.go", readFile(t, filepath.Join(dir, "cmd", "schema.go")),
		`"github.com/epistemer/cfgx"`, "//go:generate cfgx gen", "var Schema = cfgx.Define(",
		`cfgx.Bool("debug")`, `cfgx.Int("port")`, `Scope("serve")`, `cfgx.Object("db"`, `Scope("serve", "migrate")`)
	assertContains(t, "config.gen.go", readFile(t, filepath.Join(dir, "cmd", "config.gen.go")),
		"// Source: cmd/schema.go", "package cmd", "func LoadServe(", "func LoadMigrate(")
	assertContains(t, "config.1", readFile(t, filepath.Join(dir, "docs", "config.1")), `.\" myapp `)
}

func TestInit_DefaultDir(t *testing.T) {
	chdir(t, t.TempDir())
	if _, errOut, code := runCLI(t, "init", "--no-tidy", "example.com/org/tool"); code != 0 {
		t.Fatalf("init: code %d\n%s", code, errOut)
	}
	assertExists(t, filepath.Join("tool", "go.mod"), filepath.Join("tool", "cmd", "config.gen.go"))
	assertContains(t, "go.mod", readFile(t, filepath.Join("tool", "go.mod")), "module example.com/org/tool\n")
}

func TestInit_SkipsExisting(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "myapp")
	if _, errOut, code := runCLI(t, "init", "--dir", dir, "--no-tidy", "github.com/test/myapp"); code != 0 {
		t.Fatalf("init: code %d\n%s", code, errOut)
	}
	root := filepath.Join(dir, "cmd", "root.go")
	writeFile(t, root, "// edited\n")

	out, errOut, code := runCLI(t, "init", "--dir", dir, "--no-tidy", "github.com/test/myapp")
	if code != 0 {
		t.Fatalf("second init: code %d\n%s", code, errOut)
	}
	for _, f := range scaffoldFiles {
		assertContains(t, "stdout", out, "⚠ skipped "+f+" (already exists; use --force to overwrite)\n")
	}
	if got := readFile(t, root); got != "// edited\n" {
		t.Errorf("root.go overwritten without --force: %q", got)
	}

	out, errOut, code = runCLI(t, "init", "--dir", dir, "--no-tidy", "--force", "github.com/test/myapp")
	if code != 0 {
		t.Fatalf("init --force: code %d\n%s", code, errOut)
	}
	if strings.Contains(out, "skipped") {
		t.Errorf("--force skipped files:\n%s", out)
	}
	assertContains(t, "root.go", readFile(t, root), "func NewRootCmd()")
}

func TestInit_Args(t *testing.T) {
	if _, _, code := runCLI(t, "init"); code != 1 {
		t.Errorf("init without module path: code %d, want 1", code)
	}
	if _, _, code := runCLI(t, "init", "--dir", t.TempDir(), "bad path"); code != 1 {
		t.Errorf("init with invalid module path: code %d, want 1", code)
	}
}

// TestInit_Builds scaffolds a project against this repository, runs go mod
// tidy, and checks that it builds, vets cleanly and lists its flags.
func TestInit_Builds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping build test in -short mode")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("skipping: go command not found in PATH")
	}
	dir := filepath.Join(t.TempDir(), "myapp")
	out, errOut, code := runCLI(t, "init", "--dir", dir, "--cfgx-replace", repoRoot(t), "github.com/test/myapp")
	if code != 0 {
		if strings.Contains(errOut, "go mod tidy") {
			t.Skipf("skipping: go mod tidy failed (network or module cache unavailable?):\n%s", errOut)
		}
		t.Fatalf("init: code %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	assertContains(t, "stdout", out, "✓ ran go mod tidy")
	assertExists(t, filepath.Join(dir, "go.sum"))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	goCmd := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "go", args...)
		cmd.Dir = dir
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, b)
		}
		return string(b)
	}
	goCmd("build", "./...")
	if out := goCmd("vet", "./..."); strings.TrimSpace(out) != "" {
		t.Errorf("go vet output:\n%s", out)
	}
	help := goCmd("run", ".", "serve", "--help")
	assertContains(t, "serve --help", help, "--port", "--debug", "--config")

	run := goCmd("run", ".", "serve", "--port", "9001")
	assertContains(t, "serve", run, "serving on :9001", "db=localhost:5432/myapp")
}

func TestModuleVersion(t *testing.T) {
	tests := []struct{ in, want string }{
		{"v1.2.3", "v1.2.3"},
		{"v0.0.0-20260929181614-1dd5da40bccc", "v0.0.0-20260929181614-1dd5da40bccc"},
		{"v0.0.0-20260929181614-1dd5da40bccc+dirty", "v0.0.0"},
		{"dev", "v0.0.0"},
		{"", "v0.0.0"},
	}
	for _, tt := range tests {
		if got := moduleVersion(tt.in); got != tt.want {
			t.Errorf("moduleVersion(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
