package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// docsFormats lists the formats accepted by cfgx docs, in output order.
var docsFormats = []string{"md", "man", "json-schema"}

type docsOptions struct {
	schema string
	format string
	out    string
}

func newDocsCmd() *cobra.Command {
	var o docsOptions
	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Generate documentation artifacts only",
		Long: `docs runs only the requested documentation generators and writes their
output to the output directory: config.md (md), config.1 (man) and
config.schema.json (json-schema).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDocs(cmd.OutOrStdout(), cmd.ErrOrStderr(), o)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&o.schema, "schema", "s", "", "path to schema Go file (default: auto-discover)")
	f.StringVarP(&o.format, "format", "f", strings.Join(docsFormats, ","), "comma-separated formats: md,man,json-schema")
	f.StringVarP(&o.out, "out", "o", "", "output directory (default: docs/ in the module root)")
	return cmd
}

func runDocs(stdout, stderr io.Writer, o docsOptions) error {
	want := map[string]bool{}
	for _, f := range strings.Split(o.format, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if _, ok := artifactKinds[f]; !ok || artifactKinds[f].docsName == "" {
			return fmt.Errorf("unknown format %q (valid: %s)", f, strings.Join(docsFormats, ", "))
		}
		want[f] = true
	}
	if len(want) == 0 {
		return fmt.Errorf("no format given (valid: %s)", strings.Join(docsFormats, ", "))
	}
	p, err := loadProject(stderr, o.schema, 1)
	if err != nil {
		return err
	}
	outDir := filepath.Join(p.root, "docs")
	if o.out != "" {
		if outDir, err = filepath.Abs(o.out); err != nil {
			return err
		}
	}
	var files []outFile
	for _, f := range docsFormats {
		if !want[f] {
			continue
		}
		a := artifactKinds[f]
		data, err := a.gen(p)
		if err != nil {
			return err
		}
		files = append(files, outFile{path: filepath.Join(outDir, a.docsName), data: data})
	}
	return writeFiles(stdout, files, false)
}
