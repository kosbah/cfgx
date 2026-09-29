// Command webapp is an example cobra/viper application configured with cfgx.
// See README.md.
package main

import (
	"os"

	"github.com/example/cfgx/examples/webapp/cmd"
)

func main() {
	if err := cmd.NewRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
