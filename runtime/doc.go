// Package runtime provides the thin runtime helpers used by cfgx-generated code
// and by the cfgx CLI.
//
// Application code does not normally import it: generated config.gen.go files
// use the [Watcher], and `cfgx validate` uses [ValidateSchema].
//
// # Validators
//
// [Registry] holds named validator functions. [DefaultRegistry] is
// pre-populated with the parameterless built-ins under their SchemaIR names
// ("email", "url", "port", "base64", "duration", "semver"); the parameterized
// built-ins ("port_range", "regex", "enum", "range") are built from
// ValidatorIR params. Custom validators may be added with [Registry.Register].
// The constructors [Email], [URL], [Port], [PortRange], [Base64], [Regex],
// [Duration], [Semver], [Enum] and [Range] share their implementation with the
// root cfgx package, so a value is accepted or rejected identically by
// generated Load functions and by the CLI.
//
// [ValidateAll] runs the built-in validators of a SchemaIR against a
// cfgx.ValueSource (a *viper.Viper satisfies it). [ValidateSchema] adds the
// Required, DependsOn and MutuallyExclusive checks, mirroring
// cfgx.Schema.Validate, but works from the IR alone: custom Validate closures
// are not run and entries with a Getter are skipped.
//
// Generated Load functions do not use the registry; they validate through
// cfgx.Schema.ValidateScope, which runs the schema's own closures.
//
// # IsLive watcher
//
// [Watcher] keeps IsLive config entries up to date while the program runs. It
// polls environment variables every interval ([DefaultInterval] unless set),
// polls Getter-only entries, and uses fsnotify to watch config files, re-reading
// the file through the function given to [Watcher.SetReloader] before calling
// the entries' Getters. New values are stored atomically and OnChange callbacks
// run on the watcher goroutine.
//
// When a schema has IsLive entries, every generated Load function starts a
// Watcher for them and the generated Close method stops it.
package runtime
