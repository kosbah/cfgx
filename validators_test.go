package cfgx

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
		{"Email", Email(), []string{"user@example.com", "a.b+c@d.io"}, []string{"", "nope", "Bob <bob@x.com>", "a@"}},
		{"URL", URL(), []string{"https://example.com", "postgres://u@h:5432/db", "mailto:a@b.c"}, []string{"", "example.com", "/path", "://x"}},
		{"Base64", Base64(), []string{"aGVsbG8=", "", "-_8=", "+/8="}, []string{"not base64!", "aGVsbG8"}},
		{"Regex", Regex(`^[a-z]+$`), []string{"abc"}, []string{"ABC", "a1", ""}},
		{"DurationValidator", DurationValidator(), []string{"30s", "1h5m", "0"}, []string{"", "30", "soon"}},
		{"Semver", Semver(), []string{"1.2.3", "v0.1.0", "1.0.0-rc.1+build.5"}, []string{"1.2", "01.2.3", "v1", "1.2.3-"}},
		{"Enum", Enum("text", "json"), []string{"text", "json", "JSON", "Text"}, []string{"", "yaml"}},
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
	for _, v := range []float64{-0.1, 100.01} {
		if err := r(v); err == nil {
			t.Errorf("Range(%v) = nil, want error", v)
		}
	}
}

func TestRegexPanicsOnBadPattern(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	Regex("(")
}

func TestRangeRejectsNaN(t *testing.T) {
	if err := Range(0, 1)(math.NaN()); err == nil {
		t.Error("Range(0, 1)(NaN) = nil, want error")
	}
}

func TestPortRangePanicsOnBadBounds(t *testing.T) {
	for _, b := range [][2]int{{0, 10}, {1, 65536}, {100, 99}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("PortRange(%d, %d) did not panic", b[0], b[1])
				}
			}()
			PortRange(b[0], b[1])
		}()
	}
}
