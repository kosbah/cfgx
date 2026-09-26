// Package ir defines the SchemaIR intermediate representation used to pass
// schema data between the parser and the code/artifact generators.
//
// SchemaIR is a fully JSON-serializable snapshot of a cfgx.Schema. It contains
// only data — no Go closures or function references. Runtime-only properties
// (Validate, OnChange, Getter) are represented as boolean sentinels.
//
// The authoritative specification for all types, invariants, and the JSON format
// is in docs/design/schema-ir-spec.md.
//
// The parser guarantees all SchemaIR invariants before returning. Generators
// may assume all invariants hold without re-validating.
package ir
