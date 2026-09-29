package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/epistemer/cfgx/examples/webapp/pkg/database"
)

func newMigrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Run database migrations",
		RunE: func(cmd *cobra.Command, _ []string) error {
			// LoadMigrate checks only the values migrate uses: it does not
			// fail on serve-only settings such as tls-cert-path.
			cfg, err := LoadMigrate(Schema, viper.GetViper())
			if err != nil {
				return err
			}
			defer cfg.Close()

			if err := database.New(cfg.Db()).Migrate(cmd.Context()); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: migrated %s:%d/%s\n",
				cfg.AppName(), cfg.Db().Host(), cfg.Db().Port(), cfg.Db().Name())
			return nil
		},
	}
}
