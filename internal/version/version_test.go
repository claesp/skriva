package version

import (
	"regexp"
	"runtime/debug"
	"testing"
)

func TestRevision(t *testing.T) {
	info := func(settings ...debug.BuildSetting) func() (*debug.BuildInfo, bool) {
		return func() (*debug.BuildInfo, bool) { return &debug.BuildInfo{Settings: settings}, true }
	}
	none := func() (*debug.BuildInfo, bool) { return nil, false }
	full := "3f9a2c1d4e5b6a7980112233445566778899aabb"
	for _, c := range []struct {
		name, set string
		bi        func() (*debug.BuildInfo, bool)
		want      string
	}{
		{"linker flag", full, none, "3f9a2c1d4e5b"},
		{"short linker flag", "3f9a2c1", none, "3f9a2c1"},
		{"build info", "", info(debug.BuildSetting{Key: "vcs.revision", Value: full}), "3f9a2c1d4e5b"},
		{"changed work tree", "", info(debug.BuildSetting{Key: "vcs.revision", Value: full},
			debug.BuildSetting{Key: "vcs.modified", Value: "true"}), "3f9a2c1d4e5b-dirty"},
		{"no Git", "", info(), "dev"},
		{"no build info", "", none, "dev"},
	} {
		if got := revision(c.set, c.bi); got != c.want {
			t.Errorf("%s: revision = %q, want %q", c.name, got, c.want)
		}
	}
}

// Version must be major.minor. scripts/check-release.sh reads it.
func TestVersion(t *testing.T) {
	if !regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`).MatchString(Version) {
		t.Fatalf("Version %q is not major.minor", Version)
	}
	if got := (Info{Version: "1.2", Revision: "abc"}).String(); got != "1.2.abc" {
		t.Fatalf("String = %q", got)
	}
}
