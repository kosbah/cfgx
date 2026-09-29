package cfgx

import "github.com/example/cfgx/internal/validate"

// The built-in validators below are thin wrappers over the shared
// implementation in internal/validate, which the runtime package also uses, so
// the DSL, generated Load() and the CLI agree on every value.

// Email returns a validator that accepts a bare RFC 5322 address such as
// "user@example.com" (no display name).
func Email() func(string) error { return validate.Email() }

// URL returns a validator that accepts values url.Parse can parse and that
// have a scheme, e.g. "https://example.com".
func URL() func(string) error { return validate.URL() }

// Port returns a validator that accepts TCP/UDP port numbers from 1 to 65535.
func Port() func(int) error { return validate.Port() }

// PortRange returns a validator that accepts port numbers in [min, max]. It
// panics if min < 1, max > 65535 or min > max.
func PortRange(min, max int) func(int) error { return validate.PortRange(min, max) }

// Base64 returns a validator that accepts padded base64 in either the standard
// or the URL-safe alphabet.
func Base64() func(string) error { return validate.Base64() }

// Regex returns a validator that accepts values matching pattern. It panics if
// pattern does not compile.
func Regex(pattern string) func(string) error { return validate.Regex(pattern) }

// DurationValidator returns a validator that accepts strings parseable by
// time.ParseDuration. It is named DurationValidator because Duration is the
// duration entry constructor.
func DurationValidator() func(string) error { return validate.Duration() }

// Semver returns a validator that accepts Semantic Versioning 2.0.0 strings
// (patch required), optionally prefixed with "v".
func Semver() func(string) error { return validate.Semver() }

// Enum returns a validator that accepts only the listed values, compared
// case-insensitively.
func Enum(values ...string) func(string) error { return validate.Enum(values...) }

// Range returns a validator that accepts values in [min, max]. NaN is rejected.
func Range(min, max float64) func(float64) error { return validate.Range(min, max) }
