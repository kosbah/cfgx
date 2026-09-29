package main

import (
	"io"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// version is the cfgx version. It can be set at build time with
// -ldflags "-X main.version=v1.2.3"; otherwise the module version from the
// build info is used, or "dev".
var version = ""

func cfgxVersion() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}

// newRootCmd builds the cfgx command tree writing to stdout and stderr.
// Commands never call os.Exit; failures are returned as errors (see exitCode).
func newRootCmd(stdout, stderr io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:   "cfgx",
		Short: "Typed configuration for cobra/viper applications",
		Long: `cfgx scaffolds cobra/viper+cfgx projects, generates typed Go config code and
example files from a schema, validates configurations against a schema, and
generates configuration documentation.`,
		Version:       cfgxVersion(),
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.AddCommand(
		newInitCmd(),
		newGenCmd(),
		newValidateCmd(),
		newDocsCmd(),
	)
	return root
}
