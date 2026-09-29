//go:build e2e

// Package e2e runs the whole cfgx pipeline end to end against the
// examples/webapp project: cfgx gen → go build → cfgx validate → Load.
//
// The Load-level tests live in examples/webapp/cmd (a separate module this
// module cannot import); TestWebappE2E runs them with go test.
//
// Run with: go test -tags e2e -race -count=1 ./_test/e2e/...
package e2e

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

var (
	repoRoot   string // absolute path of the cfgx repository
	webappDir  string // absolute path of examples/webapp
	cfgxBin    string // the cfgx binary built by TestMain
	goBin      string // the go command
	schemaPath string // examples/webapp/cmd/schema.go
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	var err error
	if repoRoot, err = filepath.Abs("../.."); err != nil {
		panic(err)
	}
	webappDir = filepath.Join(repoRoot, "examples", "webapp")
	schemaPath = filepath.Join(webappDir, "cmd", "schema.go")
	if goBin, err = exec.LookPath("go"); err != nil {
		panic("e2e: go command not found in PATH")
	}
	tmp, err := os.MkdirTemp("", "cfgx-e2e-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)
	cfgxBin = filepath.Join(tmp, "cfgx")
	cmd := exec.Command(goBin, "build", "-o", cfgxBin, "./cmd/cfgx")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		panic("e2e: building cfgx failed: " + err.Error() + "\n" + string(out))
	}
	return m.Run()
}

// result is the outcome of running a command.
type result struct {
	code   int
	stdout string
	stderr string
}

// runCmd runs name with args in dir and the given environment and returns its
// exit code and output. It fails the test if the command cannot be started.
func runCmd(t *testing.T, timeout time.Duration, dir string, env []string, name string, args ...string) result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	r := result{stdout: stdout.String(), stderr: stderr.String()}
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		r.code = ee.ExitCode()
	default:
		t.Fatalf("running %s %s: %v\n%s%s", name, strings.Join(args, " "), err, r.stdout, r.stderr)
	}
	return r
}

// configEnvPrefixes are the prefixes of the environment variables the webapp
// schema binds; they are removed from the inherited environment so that the
// developer's shell cannot influence the results.
var configEnvPrefixes = []string{"APP_", "DB_", "SMTP_", "EMAIL_"}

// baseEnv returns the current environment without webapp config variables,
// plus GOWORK=off.
func baseEnv() []string {
	var env []string
next:
	for _, kv := range os.Environ() {
		for _, p := range configEnvPrefixes {
			if strings.HasPrefix(kv, p) {
				continue next
			}
		}
		env = append(env, kv)
	}
	return append(env, "GOWORK=off")
}

// readEnvFile returns the KEY=VALUE lines of a .env file, skipping blank
// lines and comments.
func readEnvFile(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.Contains(line, "=") {
			t.Fatalf("%s: malformed line %q", path, line)
		}
		out = append(out, line)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGen_ProducesCompilableOutput(t *testing.T) {
	dir := t.TempDir()
	copyFile(t, schemaPath, filepath.Join(dir, "cmd", "schema.go"))
	copyFile(t, filepath.Join(webappDir, "go.sum"), filepath.Join(dir, "go.sum"))
	mod, err := os.ReadFile(filepath.Join(webappDir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	// Point the replace directive at this repository.
	re := regexp.MustCompile(`(?m)^replace github.com/example/cfgx => .*$`)
	if !re.Match(mod) {
		t.Fatal("examples/webapp/go.mod has no replace directive for github.com/example/cfgx")
	}
	mod = re.ReplaceAll(mod, []byte("replace github.com/example/cfgx => "+repoRoot))
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), mod, 0o644); err != nil {
		t.Fatal(err)
	}

	r := runCmd(t, 2*time.Minute, dir, baseEnv(), cfgxBin,
		"gen", "--schema", filepath.Join(dir, "cmd", "schema.go"), "--pkg", "cmd", "--out-dir", dir)
	if r.code != 0 {
		t.Fatalf("cfgx gen exited %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	for _, f := range []string{"cmd/config.gen.go", ".env.example", "config.example.yaml", "config.schema.json", "docs/config.md", "docs/config.1"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err != nil {
			t.Errorf("cfgx gen did not write %s: %v", f, err)
		}
	}

	r = runCmd(t, 5*time.Minute, dir, append(baseEnv(), "GOFLAGS=-mod=mod"), goBin, "build", "./cmd")
	if r.code != 0 {
		t.Fatalf("go build ./cmd exited %d:\n%s%s", r.code, r.stdout, r.stderr)
	}

	// The committed config.gen.go is what cfgx gen produces for the schema.
	got, err := os.ReadFile(filepath.Join(dir, "cmd", "config.gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(webappDir, "cmd", "config.gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("examples/webapp/cmd/config.gen.go is out of date; run go generate ./cmd/... in examples/webapp")
	}
}

// validate runs cfgx validate --env on the webapp schema with the variables
// of envFile.
func validate(t *testing.T, envFile string) result {
	t.Helper()
	env := append(baseEnv(), readEnvFile(t, filepath.Join(webappDir, "testdata", envFile))...)
	return runCmd(t, time.Minute, webappDir, env, cfgxBin, "validate", "--schema", schemaPath, "--env")
}

func TestValidate_AcceptsValidConfig(t *testing.T) {
	r := validate(t, "valid.env")
	if r.code != 0 {
		t.Fatalf("cfgx validate exited %d, want 0\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
}

func TestValidate_RejectsMissingRequired(t *testing.T) {
	r := validate(t, "invalid_missing.env")
	if r.code != 1 {
		t.Fatalf("cfgx validate exited %d, want 1\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	out := r.stdout + r.stderr
	for _, want := range []string{"db.name", "db.password"} {
		if !strings.Contains(r.stderr, "✗ "+want) {
			t.Errorf("output does not report %s:\n%s", want, out)
		}
	}
}

func TestValidate_RejectsWrongTypes(t *testing.T) {
	r := validate(t, "invalid_types.env")
	if r.code != 1 {
		t.Fatalf("cfgx validate exited %d, want 1\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stderr, "✗ port") {
		t.Errorf("output does not report port:\n%s%s", r.stdout, r.stderr)
	}
}

// TestBusinessLogicImportsNoCfgx checks the invariant that examples/webapp/pkg
// never mentions cfgx or viper (equivalent to
// grep -r "cfgx\|viper" examples/webapp/pkg/ printing nothing).
func TestBusinessLogicImportsNoCfgx(t *testing.T) {
	root := filepath.Join(webappDir, "pkg")
	n := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		n++
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, "cfgx") || strings.Contains(line, "viper") {
				rel, _ := filepath.Rel(repoRoot, path)
				t.Errorf("%s:%d mentions cfgx or viper: %s", rel, i+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatalf("no files found under %s", root)
	}
}

// TestWebappE2E runs the Load-level e2e tests of examples/webapp (populating
// fields, scoped loads, DependsOn and the IsLive watcher) with the race
// detector.
func TestWebappE2E(t *testing.T) {
	r := runCmd(t, 5*time.Minute, webappDir, baseEnv(), goBin, "test", "-tags", "e2e", "-race", "-count=1", "./...")
	if r.code != 0 {
		t.Fatalf("go test in examples/webapp exited %d:\n%s%s", r.code, r.stdout, r.stderr)
	}
	t.Logf("examples/webapp:\n%s", r.stdout)
}
