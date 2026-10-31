package slug

import (
	"strings"
	"testing"
)

func TestMake(t *testing.T) {
	tests := map[string]string{
		"Home":                   "home",
		"  Hello,   World! ":     "hello-world",
		"Översikt för 2026":      "oversikt-for-2026",
		"a--b__c":                "a-b-c",
		"!!!":                    "",
		"日本語":                    "",
		strings.Repeat("a", 300): strings.Repeat("a", MaxLength),
	}
	for in, want := range tests {
		if got := Make(in); got != want {
			t.Errorf("Make(%q) = %q, want %q", in, got, want)
		}
		if want != "" && !Valid(want) {
			t.Errorf("Valid(%q) = false", want)
		}
	}
}

func TestSlugifyNoTrailingDash(t *testing.T) {
	in := strings.Repeat("a", MaxLength-1) + " b"
	got := Make(in)
	if !Valid(got) {
		t.Fatalf("Make(%q) = %q is not valid", in, got)
	}
}

func TestValid(t *testing.T) {
	for _, s := range []string{"", "-a", "a-", "A", "a b", "a/b", "../x", "a--b"} {
		if Valid(s) {
			t.Errorf("Valid(%q) = true", s)
		}
	}
}

func TestNormalize(t *testing.T) {
	tests := map[string]string{
		"getting-started":                "getting-started",
		"Getting-Started":                "getting-started",
		"TEST":                           "test",
		"A1-B2":                          "a1-b2",
		"\u212Aey":                       "",
		"a b":                            "",
		"-a":                             "",
		"../x":                           "",
		"":                               "",
		strings.Repeat("A", MaxLength+1): "",
	}
	for in, want := range tests {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}
