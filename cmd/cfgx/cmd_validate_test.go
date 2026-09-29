package main

import (
	"path/filepath"
	"strings"
	"testing"
)

const validYAML = `signing_key: aGVsbG8=
port: 9000
log_format: json
hosts:
  - http://one
  - http://two
db:
  host: db.internal
  password: hunter2-secret
`

const invalidYAML = `port: 70000
db:
  host: db.internal
`

func writeConfig(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	writeFile(t, p, content)
	return p
}

func TestValidate_AcceptsValidConfig(t *testing.T) {
	cfg := writeConfig(t, "config.yaml", validYAML)
	out, errOut, code := runCLI(t, "validate", "-s", testSchema(t, "full.go"), "-c", cfg)
	if code != 0 {
		t.Fatalf("code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	if errOut != "" {
		t.Errorf("stderr = %q, want empty", errOut)
	}
	assertContains(t, "stdout", out, "✓ port\n", "✓ db.password\n", "✓ db.pool.max_open\n", "✓ hosts\n")
	if strings.Contains(out, " = ") || strings.Contains(out, "hunter2") {
		t.Errorf("values printed without --show-values:\n%s", out)
	}
	if strings.Contains(out, "db.dsn") {
		t.Errorf("computed entry listed:\n%s", out)
	}
}

func TestValidate_ShowValues(t *testing.T) {
	cfg := writeConfig(t, "config.yaml", validYAML)
	out, errOut, code := runCLI(t, "validate", "-s", testSchema(t, "full.go"), "-c", cfg, "--show-values")
	if code != 0 {
		t.Fatalf("code %d, want 0\n%s", code, errOut)
	}
	assertContains(t, "stdout", out,
		"✓ port = 9000\n",
		`✓ name = "cfgx"`+"\n",
		`✓ db.host = "db.internal"`+"\n",
		"✓ db.password = [redacted]\n",
		"✓ signing_key = [redacted]\n",
		"✓ timeout = 30s\n",
		"✓ sample_rate = 0.5\n",
		`✓ hosts = ["http://one", "http://two"]`+"\n",
		"✓ admin_email = (not set)\n",
	)
	for _, secret := range []string{"hunter2", "aGVsbG8="} {
		if strings.Contains(out+errOut, secret) {
			t.Errorf("sensitive value %q printed:\n%s", secret, out)
		}
	}
}

func TestValidate_DetectsErrors(t *testing.T) {
	cfg := writeConfig(t, "config.yaml", invalidYAML)
	out, errOut, code := runCLI(t, "validate", "-s", testSchema(t, "full.go"), "-c", cfg)
	if code != 1 {
		t.Fatalf("code %d, want 1\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	assertContains(t, "stderr", errOut,
		"✗ db.password: required but not set\n",
		"✗ signing_key: required but not set\n",
		"✗ port: ",
		"\n3 errors found.\n",
	)
	assertContains(t, "stdout", out, "✓ db.host\n")
	if strings.Contains(out, "port\n") && strings.Contains(out, "✓ port\n") {
		t.Errorf("failing key reported as valid:\n%s", out)
	}
}

func TestValidate_SingleErrorAndConstraint(t *testing.T) {
	cfg := writeConfig(t, "config.json",
		`{"signing_key": "aGVsbG8=", "db": {"password": "x"}, "json_output": true, "text_output": true}`)
	_, errOut, code := runCLI(t, "validate", "-s", testSchema(t, "full.go"), "-c", cfg)
	if code != 1 {
		t.Fatalf("code %d, want 1\n%s", code, errOut)
	}
	assertContains(t, "stderr", errOut, "✗ json_output, text_output: mutually exclusive", "\n1 error found.\n")
}

func TestValidate_Env(t *testing.T) {
	schema := testSchema(t, "full.go")
	t.Setenv("SIGNING_KEY", "aGVsbG8=")
	t.Setenv("DB_PASSWORD", "from-env")
	t.Setenv("APP_PORT", "8443")

	// Without --env the environment is ignored.
	if _, _, code := runCLI(t, "validate", "-s", schema); code != 1 {
		t.Fatalf("without --env: code %d, want 1", code)
	}
	out, errOut, code := runCLI(t, "validate", "-s", schema, "--env", "--show-values")
	if code != 0 {
		t.Fatalf("with --env: code %d, want 0\n%s", code, errOut)
	}
	assertContains(t, "stdout", out, "✓ port = 8443\n", "✓ db.password = [redacted]\n")

	t.Setenv("APP_PORT", "80")
	_, errOut, code = runCLI(t, "validate", "-s", schema, "-e")
	if code != 1 {
		t.Fatalf("bad env: code %d, want 1", code)
	}
	assertContains(t, "stderr", errOut, "✗ port: ", "1 error found.")
}

func TestValidate_ToolErrors(t *testing.T) {
	schema := testSchema(t, "full.go")
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"missing schema", []string{"-s", filepath.Join(t.TempDir(), "nope.go")}, "nope.go"},
		{"missing config", []string{"-s", schema, "-c", filepath.Join(t.TempDir(), "nope.yaml")}, "nope.yaml"},
		{"bad config", []string{"-s", schema, "-c", writeConfig(t, "c.yaml", "port: [unclosed\n")}, "c.yaml"},
		{"parse error", []string{"-s", testSchema(t, "invalid_duplicate.go")}, `✗ parse error: `},
		{"bad flag", []string{"--nope"}, "unknown flag"},
		{"extra arg", []string{"-s", schema, "extra"}, "unknown command"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, errOut, code := runCLI(t, append([]string{"validate"}, tt.args...)...)
			if code != 2 {
				t.Fatalf("code %d, want 2\nstderr:\n%s", code, errOut)
			}
			assertContains(t, "stderr", errOut, tt.want)
		})
	}
}
