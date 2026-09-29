// Package cmd holds the webapp commands and their configuration.
//
// The configuration is declared once in schema.go; `go generate ./cmd/...`
// turns it into config.gen.go, which provides RegisterFlags, BindViper and
// one typed Load function per command (Load, LoadServe, LoadMigrate).
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// cfgFile is the path given with --config.
var cfgFile string

// NewRootCmd returns the webapp root command with all subcommands.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:          "webapp",
		Short:        "Example web application configured with cfgx",
		SilenceUsage: true,
	}
	root.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (YAML, JSON or TOML)")

	// RegisterFlags (generated) adds a flag for every schema entry that
	// declares one; BindViper binds those flags, env vars and defaults.
	RegisterFlags(root, Schema)
	cobra.OnInitialize(initConfig)

	root.AddCommand(newServeCmd(), newMigrateCmd())
	return root
}

// initConfig reads the config file (if any) and binds flags, env vars and
// defaults to viper.
func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
		if err := viper.ReadInConfig(); err != nil {
			cobra.CheckErr(fmt.Errorf("reading config file: %w", err))
		}
	}
	BindViper(viper.GetViper(), Schema)
}
