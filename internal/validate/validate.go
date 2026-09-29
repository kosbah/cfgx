// Package validate holds the single implementation of cfgx's built-in
// validators. The root cfgx package and the runtime package both wrap these
// constructors, so a value is accepted or rejected identically by generated
// Load() code and by the cfgx CLI.
//
// It depends only on the standard library.
package validate

import (
	"encoding/base64"
	"fmt"
	"math"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Email returns a validator that accepts a bare RFC 5322 address such as
// "user@example.com" (no display name).
func Email() func(string) error {
	return func(v string) error {
		addr, err := mail.ParseAddress(v)
		if err != nil || addr.Address != v {
			return fmt.Errorf("%q is not a valid email address", v)
		}
		return nil
	}
}

// URL returns a validator that accepts values url.Parse can parse and that
// have a scheme, e.g. "https://example.com" or "mailto:a@b.c".
func URL() func(string) error {
	return func(v string) error {
		u, err := url.Parse(v)
		if err != nil || u.Scheme == "" {
			return fmt.Errorf("%q is not a valid URL with a scheme", v)
		}
		return nil
	}
}

// Port returns a validator that accepts TCP/UDP port numbers from 1 to 65535.
func Port() func(int) error {
	return PortRange(1, 65535)
}

// PortRange returns a validator that accepts port numbers in [min, max]. It
// panics if min < 1, max > 65535 or min > max.
func PortRange(min, max int) func(int) error {
	if min < 1 || max > 65535 || min > max {
		panic(fmt.Sprintf("cfgx: PortRange(%d, %d): bounds must satisfy 1 <= min <= max <= 65535", min, max))
	}
	return func(v int) error {
		if v < min || v > max {
			return fmt.Errorf("port %d is out of range [%d, %d]", v, min, max)
		}
		return nil
	}
}

// Base64 returns a validator that accepts padded base64 in either the standard
// or the URL-safe alphabet.
func Base64() func(string) error {
	return func(v string) error {
		_, err := base64.StdEncoding.DecodeString(v)
		if err == nil {
			return nil
		}
		if _, uerr := base64.URLEncoding.DecodeString(v); uerr == nil {
			return nil
		}
		return fmt.Errorf("value is not valid base64: %w", err)
	}
}

// Regex returns a validator that accepts values matching pattern. The pattern
// is compiled once; Regex panics if it does not compile.
func Regex(pattern string) func(string) error {
	re := regexp.MustCompile(pattern)
	return func(v string) error {
		if !re.MatchString(v) {
			return fmt.Errorf("%q does not match pattern %q", v, pattern)
		}
		return nil
	}
}

// Duration returns a validator that accepts strings parseable by
// time.ParseDuration.
func Duration() func(string) error {
	return func(v string) error {
		if _, err := time.ParseDuration(v); err != nil {
			return fmt.Errorf("%q is not a valid duration", v)
		}
		return nil
	}
}

// semverRe is the official Semantic Versioning 2.0.0 pattern, with an optional "v" prefix.
var semverRe = regexp.MustCompile(`^v?(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)` +
	`(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?` +
	`(?:\+([0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$`)

// Semver returns a validator that accepts Semantic Versioning 2.0.0 strings
// (major, minor and patch required), optionally prefixed with "v".
func Semver() func(string) error {
	return func(v string) error {
		if !semverRe.MatchString(v) {
			return fmt.Errorf("%q is not a valid semantic version", v)
		}
		return nil
	}
}

// Enum returns a validator that accepts only the listed values, compared
// case-insensitively.
func Enum(values ...string) func(string) error {
	allowed := append([]string(nil), values...)
	return func(v string) error {
		for _, a := range allowed {
			if strings.EqualFold(v, a) {
				return nil
			}
		}
		return fmt.Errorf("%q is not one of [%s]", v, strings.Join(allowed, ", "))
	}
}

// Range returns a validator that accepts values in [min, max]. NaN is rejected.
func Range(min, max float64) func(float64) error {
	return func(v float64) error {
		if math.IsNaN(v) || v < min || v > max {
			return fmt.Errorf("%v is out of range [%v, %v]", v, min, max)
		}
		return nil
	}
}
