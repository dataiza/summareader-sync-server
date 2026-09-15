package main

import (
	"os"
	"strings"
	"testing"
)

// The release workflow reads version.go and refuses to publish without a
// CHANGELOG section naming it. That check runs on a tag, which is the worst
// possible moment to discover it fails: the tag is already pushed and the
// version already public. Here it fails on the commit that forgot the section.
func TestTheChangelogNamesThisVersion(t *testing.T) {
	changelog, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	heading := "## " + version
	for _, line := range strings.Split(string(changelog), "\n") {
		if strings.TrimSpace(line) == heading {
			return
		}
	}
	t.Fatalf("CHANGELOG.md has no %q section — bump the changelog in the same commit as version.go", heading)
}

func TestVersionLineSaysWhatItIs(t *testing.T) {
	line := versionLine()
	if !strings.HasPrefix(line, "summareader-sync "+version) {
		t.Fatalf("versionLine() = %q, want it to start with the name and %q", line, version)
	}
}
