// Package runtime provides the thin runtime helpers used by cfgx-generated code.
//
// It is imported by generated config.gen.go files — not by application code directly.
//
// # Validator registry
//
// The [Registry] holds named validator functions. [DefaultRegistry] is pre-populated
// with all built-in validators: Email, URL, Port, PortRange, Base64, Regex,
// Duration, Semver, Enum, Range.
//
// Custom validators may be registered with [Registry.Register].
//
// # IsLive watcher
//
// The [Watcher] monitors IsLive config entries and updates them when their source
// changes. It polls environment variables at a configurable interval and uses
// [fsnotify] for file-backed configs.
//
// The generated [Load] function sets up a Watcher for any IsLive entries in the schema.
// Applications do not interact with the Watcher directly.
package runtime
