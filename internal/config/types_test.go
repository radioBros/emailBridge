package config

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestParseSize(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		want    Size
		wantErr bool
	}{
		{"0", 0, false},
		{"1024", 1024, false},
		{"4KiB", 4 << 10, false},
		{"25MiB", 25 << 20, false},
		{"5GiB", 5 << 30, false},
		{"", 0, true},
		{"-1", 0, true},
		{"1TiB", 0, true},
		{"abc", 0, true},
	}
	for _, tc := range cases {
		got, err := ParseSize(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("ParseSize(%q): want error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseSize(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("ParseSize(%q)=%d want %d", tc.in, got, tc.want)
		}
	}
}

func TestParseRate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		want    Rate
		wantErr bool
	}{
		{"20/s", Rate{Count: 20, Period: time.Second, Burst: 20}, false},
		{"20/s,burst=40", Rate{Count: 20, Period: time.Second, Burst: 40}, false},
		{"10/min", Rate{Count: 10, Period: time.Minute, Burst: 10}, false},
		{"100/h", Rate{Count: 100, Period: time.Hour, Burst: 100}, false},
		{"", Rate{}, true},
		{"20", Rate{}, true},
		{"20/day", Rate{}, true},
		{"20/s,burst=x", Rate{}, true},
	}
	for _, tc := range cases {
		got, err := ParseRate(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("ParseRate(%q): want error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseRate(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("ParseRate(%q)=%+v want %+v", tc.in, got, tc.want)
		}
	}
}

func TestParseDuration(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"15m", 15 * time.Minute, false},
		{"900s", 900 * time.Second, false},
		{"7d", 7 * 24 * time.Hour, false},
		{"50ms", 50 * time.Millisecond, false},
		{"", 0, true},
		{"7x", 0, true},
	}
	for _, tc := range cases {
		got, err := ParseDuration(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("ParseDuration(%q): want error", tc.in)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Fatalf("ParseDuration(%q)=%v,%v want %v", tc.in, got, err, tc.want)
		}
	}
}

func TestParseBool(t *testing.T) {
	t.Parallel()
	if v, err := ParseBool("true"); err != nil || !v {
		t.Fatal(v, err)
	}
	if v, err := ParseBool("false"); err != nil || v {
		t.Fatal(v, err)
	}
	if _, err := ParseBool("yes"); err == nil {
		t.Fatal("want error")
	}
}

func TestParseStringList(t *testing.T) {
	t.Parallel()
	got := ParseStringList(" a, ,b,c ")
	if len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Fatalf("%v", got)
	}
	if ParseStringList("") != nil {
		t.Fatal("want nil")
	}
}

func TestParseCIDRList(t *testing.T) {
	t.Parallel()
	got, err := ParseCIDRList("10.0.0.0/8, 192.168.0.0/16")
	if err != nil || len(got) != 2 {
		t.Fatal(got, err)
	}
	if _, err := ParseCIDRList("not-a-cidr"); err == nil {
		t.Fatal("want error")
	}
}

func TestParseKeyRing(t *testing.T) {
	t.Parallel()
	k1 := make([]byte, 32)
	k2 := make([]byte, 32)
	for i := range k1 {
		k1[i] = byte(i)
		k2[i] = byte(255 - i)
	}
	b1 := base64.StdEncoding.EncodeToString(k1)
	b2 := base64.StdEncoding.EncodeToString(k2)
	ring, err := ParseKeyRing("kid1:" + b1 + ", kid2:" + b2)
	if err != nil || len(ring) != 2 || ring[0].Kid != "kid1" {
		t.Fatal(ring, err)
	}
	if _, err := ParseKeyRing("kid1:YmFk"); err == nil {
		t.Fatal("want 32-byte error")
	}
	if _, err := ParseKeyRing("nokid"); err == nil {
		t.Fatal("want format error")
	}
	// Secret values must not appear in error strings.
	sentinel := "SENTINEL_SECRET_VALUE_XYZ_" + strings.Repeat("A", 20)
	_, err = ParseKeyRing(sentinel)
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), sentinel) {
		t.Fatalf("secret leaked in error: %v", err)
	}
}
