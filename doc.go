// Package cfgx provides a declarative, type-safe configuration layer for
// cobra/viper applications.
//
// Define your configs once in a schema file using the DSL:
//
//	//go:generate cfgx gen
//
//	var Schema = cfgx.Define(
//	    cfgx.Int("port").Flag("--port", "-p").Env("APP_PORT").Default(8080),
//	    cfgx.Object("db",
//	        cfgx.String("host").Env("DB_HOST").Default("localhost"),
//	        cfgx.String("password").Env("DB_PASSWORD").Sensitive().Required(),
//	    ),
//	)
//
// Then run [go generate] to produce typed Go structs, cobra flag registration,
// viper bindings, and example configuration files.
//
// # Design goals
//
//   - Declarative: all configs defined in one place
//   - Type-safe: generated structs with getter methods; no map[string]any
//   - Decoupled: business logic packages define their own interfaces; the generated
//     structs satisfy them via Go's structural typing (no cfgx import needed)
//   - Zero boilerplate: cfgx gen handles cobra flags, viper binds, .env.example, etc.
//
// # Package layout
//
//   - [github.com/example/cfgx]: this package — DSL types and builder chain
//   - [github.com/example/cfgx/runtime]: thin runtime (validators, IsLive watcher)
//   - [github.com/example/cfgx/gen/parser]: go/ast-based schema parser
//   - [github.com/example/cfgx/gen/codegen]: Go code generator
//   - [github.com/example/cfgx/gen/artifacts]: artifact generators (.env, yaml, docs)
//   - [github.com/example/cfgx/cmd/cfgx]: CLI binary
package cfgx
