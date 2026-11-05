// Package buildinfo reports which build of the bot is running.
package buildinfo

import "runtime/debug"

// Version is set at build time:
//
//	go build -ldflags "-X github.com/Sonty15/SONNIE/internal/buildinfo.Version=$(git rev-parse --short HEAD)"
//
// The Dockerfile passes it through the VERSION build argument.
var Version string

// String is Version, else the VCS revision Go recorded in the binary (a
// plain `go build` inside a git checkout), else "dev".
func String() string {
	if Version != "" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		var rev, dirty string
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				if s.Value == "true" {
					dirty = "-dirty"
				}
			}
		}
		if len(rev) > 12 {
			rev = rev[:12]
		}
		if rev != "" {
			return rev + dirty
		}
	}
	return "dev"
}
