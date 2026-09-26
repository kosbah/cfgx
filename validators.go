package cfgx

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

// URL returns a validator that accepts absolute URLs with a scheme and host.
func URL() func(string) error {
	return func(v string) error {
		u, err := url.Parse(v)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("%q is not a valid absolute URL", v)
		}
		return nil
	}
}

// Port returns a validator that accepts TCP/UDP port numbers from 1 to 65535.
func Port() func(int) error {
	return PortRange(1, 65535)
}

// PortRange returns a validator that accepts port numbers in [min, max].
func PortRange(min, max int) func(int) error {
	return func(v int) error {
		if v < min || v > max {
			return fmt.Errorf("port %d is out of range [%d, %d]", v, min, max)
		}
		return nil
	}
}

// Base64 returns a validator that accepts standard (padded) base64 strings.
func Base64() func(string) error {
	return func(v string) error {
		if _, err := base64.StdEncoding.DecodeString(v); err != nil {
			return fmt.Errorf("value is not valid base64: %w", err)
		}
		return nil
	}
}

// Regex returns a validator that accepts values matching pattern. It panics if
// pattern does not compile.
func Regex(pattern string) func(string) error {
	re := regexp.MustCompile(pattern)
	return func(v string) error {
		if !re.MatchString(v) {
			return fmt.Errorf("%q does not match pattern %q", v, pattern)
		}
		return nil
	}
}

// DurationValidator returns a validator that accepts strings parseable by
// time.ParseDuration. It is named DurationValidator because Duration is the
// duration entry constructor.
func DurationValidator() func(string) error {
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

// Semver returns a validator that accepts Semantic Versioning 2.0.0 strings,
// optionally prefixed with "v".
func Semver() func(string) error {
	return func(v string) error {
		if !semverRe.MatchString(v) {
			return fmt.Errorf("%q is not a valid semantic version", v)
		}
		return nil
	}
}

// Enum returns a validator that accepts only the listed values.
func Enum(values ...string) func(string) error {
	allowed := append([]string(nil), values...)
	return func(v string) error {
		for _, a := range allowed {
			if v == a {
				return nil
			}
		}
		return fmt.Errorf("%q is not one of [%s]", v, strings.Join(allowed, ", "))
	}
}

// Range returns a validator that accepts values in [min, max].
func Range(min, max float64) func(float64) error {
	return func(v float64) error {
		if math.IsNaN(v) || v < min || v > max {
			return fmt.Errorf("%v is out of range [%v, %v]", v, min, max)
		}
		return nil
	}
}
