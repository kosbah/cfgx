//go:build e2e

package cmd

import (
	"testing"

	"github.com/spf13/viper"
)

// setupViper returns a viper instance holding a complete serve configuration.
func setupViper() *viper.Viper {
	v := viper.New()
	BindViper(v, Schema)
	v.Set("port", 9090)
	v.Set("debug", false)
	v.Set("db.host", "testhost")
	v.Set("db.name", "testdb")
	v.Set("db.user", "admin")
	v.Set("db.password", "secret")
	return v
}

// BenchmarkLoad_cfgx measures LoadServe from a pre-populated viper: reading,
// validating and starting (then stopping) the IsLive watcher.
func BenchmarkLoad_cfgx(b *testing.B) {
	v := setupViper()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cfg, err := LoadServe(Schema, v)
		if err != nil {
			b.Fatal(err)
		}
		cfg.Close()
	}
}

// BenchmarkLoad_baseline reads the same fields as LoadServe directly from
// viper, without validation or a watcher.
func BenchmarkLoad_baseline(b *testing.B) {
	v := setupViper()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = v.GetString("app-name")
		_ = v.GetBool("debug")
		_ = v.GetString("output-format")
		_ = v.GetBool("json-output")
		_ = v.GetBool("text-output")
		_ = v.GetString("log-file")
		_ = v.GetBool("email-enabled")
		_ = v.GetString("smtp.host")
		_ = v.GetInt("smtp.port")
		_ = v.GetString("smtp.user")
		_ = v.GetString("smtp.password")
		_ = v.GetInt("port")
		_ = v.GetBool("tls-enabled")
		_ = v.GetString("tls-cert-path")
		_ = v.GetString("db.host")
		_ = v.GetInt("db.port")
		_ = v.GetString("db.name")
		_ = v.GetString("db.user")
		_ = v.GetString("db.password")
	}
}
