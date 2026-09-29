package testschema

import (
	"fmt"

	"github.com/example/cfgx"
)

// Schema has Computed entries with inferred and explicit type arguments.
var Schema = cfgx.Define(
	cfgx.Object("db",
		cfgx.String("host").Env("DB_HOST").Default("localhost"),
		cfgx.Int("port").Env("DB_PORT").Default(5432),
		cfgx.Computed("dsn", func(parent any) string { return fmt.Sprint(parent) }).
			Desc("Connection string"),
	),
	cfgx.Computed[int]("workers", func(parent any) int { return 4 }).Tags("runtime"),
)
