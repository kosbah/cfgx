// Package artifacts implements the non-Go artifact generators for cfgx.
//
// Each generator takes a [ir.SchemaIR] and returns formatted bytes:
//
//   - [EnvExample]: .env.example file
//   - [YAMLExample]: config.example.yaml file
//   - [JSONSchema]: config.schema.json (JSON Schema draft-07)
//   - [Markdown]: docs/config.md reference document
//   - [ManPage]: docs/config.1 groff man page fragment
//
// All generators use only the standard library — no external dependencies.
//
// See docs/agents/phase4-artifacts.prompt.md for the complete specification.
package artifacts
