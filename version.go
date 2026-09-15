package main

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// The released version, and the single place it is written.
//
// A constant in the source rather than a value stamped in by -ldflags at build
// time: this is what CHANGELOG.md is checked against before a release is
// allowed to publish, and a value that only exists inside a CI job cannot be
// checked by anything a person runs locally. The release workflow reads this
// file; it does not write it.
//
// Bump it in the same commit as the CHANGELOG section it names, and tag that
// commit `v<version>`.
const version = "0.3.4"

// Where the build came from, when there is anything to say.
//
// Go records the VCS revision in the binary itself for a build made inside a
// checkout, so this costs no build flags and no generated file. It is empty
// for `go run` and for a build from an unpacked tarball, which is why nothing
// depends on it beyond printing.
func revision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" && len(setting.Value) >= 12 {
			return setting.Value[:12]
		}
	}
	return ""
}

// versionLine is what both the command and the log line print, so a support
// question answered from one matches the other.
func versionLine() string {
	if rev := revision(); rev != "" {
		return fmt.Sprintf("summareader-sync %s (%s)", version, rev)
	}
	return "summareader-sync " + version
}

func versionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version and exit",
		Run: func(_ *cobra.Command, _ []string) {
			// Straight to stdout rather than through cobra, for the reason
			// `first-device --json` gives: PocketBase points the command's
			// writer at stderr, and `$(summareader-sync version)` in a script
			// would capture nothing at all.
			fmt.Println(versionLine())
		},
	}
}
