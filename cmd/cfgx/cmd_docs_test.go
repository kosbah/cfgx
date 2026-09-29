package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func TestDocs_Formats(t *testing.T) {
	out := filepath.Join(t.TempDir(), "d")
	stdout, errOut, code := runCLI(t, "docs", "-s", testSchema(t, "full.go"), "-f", "md,man", "-o", out)
	if code != 0 {
		t.Fatalf("code %d\n%s", code, errOut)
	}
	if got := strings.Join(dirNames(t, out), ","); got != "config.1,config.md" {
		t.Fatalf("files = %s, want config.1,config.md", got)
	}
	if n := strings.Count(stdout, "✓ wrote"); n != 2 {
		t.Errorf("got %d wrote lines, want 2:\n%s", n, stdout)
	}
	assertContains(t, "config.md", readFile(t, filepath.Join(out, "config.md")), "db.password")
}

func TestDocs_DefaultFormats(t *testing.T) {
	out := filepath.Join(t.TempDir(), "d")
	if _, errOut, code := runCLI(t, "docs", "-s", testSchema(t, "full.go"), "-o", out); code != 0 {
		t.Fatalf("code %d\n%s", code, errOut)
	}
	if got := strings.Join(dirNames(t, out), ","); got != "config.1,config.md,config.schema.json" {
		t.Fatalf("files = %s", got)
	}
}

func TestDocs_DefaultOutDir(t *testing.T) {
	dir := genProject(t)
	chdir(t, filepath.Join(dir, "cmd"))
	stdout, errOut, code := runCLI(t, "docs", "-f", "json-schema")
	if code != 0 {
		t.Fatalf("code %d\n%s", code, errOut)
	}
	assertExists(t, filepath.Join(dir, "docs", "config.schema.json"))
	assertContains(t, "stdout", stdout, "✓ wrote "+filepath.Join("..", "docs", "config.schema.json"))
}

func TestDocs_UnknownFormat(t *testing.T) {
	out := filepath.Join(t.TempDir(), "d")
	for _, f := range []string{"md,pdf", "env", ","} {
		_, errOut, code := runCLI(t, "docs", "-s", testSchema(t, "full.go"), "-f", f, "-o", out)
		if code != 1 {
			t.Fatalf("-f %q: code %d, want 1", f, code)
		}
		assertContains(t, "stderr", errOut, "md, man, json-schema")
	}
	assertMissing(t, out)
}
