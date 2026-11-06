// Package version gives the version of the program.
//
// The version has the form major.minor.revision, for example
// 0.1.3f9a2c1d4e5b. Major and minor are in Version. The revision is the
// Git commit of the build. See docs/release.md for the rules.
package version

import (
	"runtime/debug"
	"strings"
)

// Version is the major and the minor version. Change it for each
// release:
//
//   - A change of the database schema (a new file in
//     internal/db/migrations) needs a new major version.
//   - Other changes need a new minor version.
//
// scripts/check-release.sh reads this line. Keep its form.
const Version = "0.1"

// revisionLength is the number of hexadecimal digits of the revision.
const revisionLength = 12

// Revision is the Git commit of the build. The build sets it with
// -ldflags "-X github.com/claesp/skriva/internal/version.Revision=...".
// If it is empty, the revision comes from the build information that
// "go build" records in a Git work tree.
var Revision = ""

// Info is the version information of the program.
type Info struct {
	// Version is major.minor.
	Version string
	// Revision is the short Git commit, or "dev" if it is not known. A
	// build from a work tree with changes ends with "-dirty".
	Revision string
}

// String returns major.minor.revision.
func (i Info) String() string { return i.Version + "." + i.Revision }

// Get returns the version information of the program.
func Get() Info {
	return Info{Version: Version, Revision: revision(Revision, debug.ReadBuildInfo)}
}

// String returns major.minor.revision of the program.
func String() string { return Get().String() }

func revision(set string, buildInfo func() (*debug.BuildInfo, bool)) string {
	if set != "" {
		return short(set)
	}
	bi, ok := buildInfo()
	if !ok {
		return "dev"
	}
	var rev string
	var dirty bool
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "dev"
	}
	rev = short(rev)
	if dirty {
		rev += "-dirty"
	}
	return rev
}

func short(rev string) string {
	if len(rev) > revisionLength && !strings.Contains(rev, "-") {
		return rev[:revisionLength]
	}
	return rev
}
