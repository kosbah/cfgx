package validate

import (
	"math"
	"testing"
)

func TestStringValidators(t *testing.T) {
	tests := []struct {
		name string
		fn   func(string) error
		pass []string
		fail []string
	}{
		{"Email", Email(), []string{"user@example.com", "a.b+c@d.io"}, []string{"", "nope", "Bob <bob@x.com>", "user@"}},
		{"URL", URL(), []string{"https://example.com", "http://", "mailto:a@b.c"}, []string{"", "example.com", "/path", "://x"}},
		{"Base64", Base64(), []string{"", "aGVsbG8=", "+/8=", "-_8="}, []string{"not base64!", "aGVsbG8", "+_8="}},
		{"Regex", Regex(`^[a-z]+$`), []string{"abc"}, []string{"ABC", "a1", ""}},
		{"Duration", Duration(), []string{"30s", "1h5m", "0"}, []string{"", "30", "soon"}},
		{"Semver", Semver(), []string{"1.2.3", "v0.1.0", "1.0.0-rc.1+build.5"}, []string{"1.2", "01.2.3", "v1", "1.2.3-", "V1.2.3"}},
		{"Enum", Enum("text", "json"), []string{"text", "JSON", "Text"}, []string{"", "yaml"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, v := range tt.pass {
				if err := tt.fn(v); err != nil {
					t.Errorf("%s(%q) = %v, want nil", tt.name, v, err)
				}
			}
			for _, v := range tt.fail {
				if err := tt.fn(v); err == nil {
					t.Errorf("%s(%q) = nil, want error", tt.name, v)
				}
			}
		})
	}
}

func TestIntValidators(t *testing.T) {
	tests := []struct {
		name string
		fn   func(int) error
		pass []int
		fail []int
	}{
		{"Port", Port(), []int{1, 80, 65535}, []int{0, -1, 65536}},
		{"PortRange", PortRange(1024, 65535), []int{1024, 8080, 65535}, []int{1023, 65536}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, v := range tt.pass {
				if err := tt.fn(v); err != nil {
					t.Errorf("%s(%d) = %v, want nil", tt.name, v, err)
				}
			}
			for _, v := range tt.fail {
				if err := tt.fn(v); err == nil {
					t.Errorf("%s(%d) = nil, want error", tt.name, v)
				}
			}
		})
	}
}

func TestRange(t *testing.T) {
	r := Range(0, 100)
	for _, v := range []float64{0, 50.5, 100} {
		if err := r(v); err != nil {
			t.Errorf("Range(%v) = %v, want nil", v, err)
		}
	}
	for _, v := range []float64{-0.1, 100.01, math.NaN()} {
		if err := r(v); err == nil {
			t.Errorf("Range(%v) = nil, want error", v)
		}
	}
}

func TestConstructorPanics(t *testing.T) {
	tests := map[string]func(){
		"PortRange min<1":   func() { PortRange(0, 10) },
		"PortRange max>max": func() { PortRange(1, 65536) },
		"PortRange min>max": func() { PortRange(100, 99) },
		"Regex bad pattern": func() { Regex("(") },
	}
	for name, fn := range tests {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected panic")
				}
			}()
			fn()
		})
	}
}
