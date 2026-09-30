package catalog

import "testing"

func TestNormalizeName(t *testing.T) {
	left := normalizeName("  Cafe\u0301   Artist ")
	right := normalizeName("café artist")
	if left != right {
		t.Fatalf("normalized %q and %q", left, right)
	}
	if left != "café artist" {
		t.Fatalf("normalized = %q", left)
	}
}

func TestNormalizeISRC(t *testing.T) {
	got, err := normalizeISRC(" us-rc1-7607839 ")
	if err != nil {
		t.Fatal(err)
	}
	if got != "USRC17607839" {
		t.Fatalf("isrc = %s", got)
	}
	if _, err := normalizeISRC("US-RC1-760783"); err == nil {
		t.Fatal("short isrc was accepted")
	}
	got, err = normalizeISRC("  ")
	if err != nil || got != "" {
		t.Fatalf("empty isrc = %q, %v", got, err)
	}
}
