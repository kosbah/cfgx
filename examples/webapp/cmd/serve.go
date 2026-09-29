package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/example/cfgx/examples/webapp/pkg/database"
	"github.com/example/cfgx/examples/webapp/pkg/server"
)

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Start the HTTP server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			// LoadServe validates the values serve uses and returns a
			// *cfgx.ValidationErrors listing each problem with its field path.
			cfg, err := LoadServe(Schema, viper.GetViper())
			if err != nil {
				return err
			}
			defer cfg.Close() // stops the watcher that keeps IsLive values current

			db := database.New(cfg.Db())     // *DbConfig satisfies database.Config
			srv := server.New(cfg, cfg.Db()) // *ServeConfig satisfies server.Config
			if err := db.Ping(cmd.Context()); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: serving on :%d (tls=%t, debug=%t), database %s\n",
				cfg.AppName(), cfg.Port(), cfg.TLSEnabled(), cfg.Debug(), cfg.Db().Host())
			return srv.Run(cmd.Context())
		},
	}
}
