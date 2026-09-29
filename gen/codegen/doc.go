// Package codegen implements the Go source code generator for cfgx.
//
// [Generate] takes an [ir.SchemaIR] and [Options] (package name, cfgx module
// path, source file name) and produces a formatted config.gen.go containing:
//
//   - a root Config struct with the entries shared by all commands, and one
//     <Scope>Config struct per scope (e.g. ServeConfig) that embeds Config and
//     adds the entries scoped to that command;
//   - one <Object>Config struct per Object entry (e.g. DbConfig);
//   - unexported fields with getter methods for every entry (cfg.Port(),
//     cfg.Db().Host()); IsLive entries are stored in atomic.Values, Sensitive
//     entries in a type that redacts itself when printed or marshalled (the
//     getter still returns the plain string), Computed entries are evaluated
//     on every call;
//   - RegisterFlags(cmd, schema), which registers every entry with a flag as a
//     persistent flag on cmd;
//   - BindViper(v, schema), which binds those flags, the env vars and defaults;
//   - Load(schema, v) and one Load<Scope>(schema, v) per scope, which read v,
//     call Getters, validate with cfgx.Schema.ValidateScope and, if the schema
//     has IsLive entries, start a runtime.Watcher for them; Close stops it.
//
// The Go source is emitted from the embedded templates/config.go.tmpl with
// text/template and always passed through go/format.Source before it is
// returned.
package codegen
