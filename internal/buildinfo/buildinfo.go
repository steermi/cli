// Package buildinfo reports the CLI's version. Release builds set the
// variables with -ldflags (see .goreleaser.yaml); `go install …@vX.Y.Z` builds
// fall back to the module version and VCS data embedded by the Go toolchain.
package buildinfo

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

var (
	version = ""
	commit  = ""
	date    = ""
)

// Info describes the running binary.
type Info struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Date    string `json:"date,omitempty"`
	Go      string `json:"go"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

// Get returns the build information, "dev" when nothing identifies the build.
func Get() Info {
	i := Info{Version: version, Commit: commit, Date: date, Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH}
	dirty := false
	if bi, ok := debug.ReadBuildInfo(); ok && commit == "" {
		if i.Version == "" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			i.Version = bi.Main.Version
		}
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				i.Commit = s.Value
			case "vcs.time":
				if i.Date == "" {
					i.Date = s.Value
				}
			case "vcs.modified":
				dirty = s.Value == "true"
			}
		}
	}
	if i.Version == "" {
		i.Version = "dev"
	}
	if len(i.Commit) > 12 {
		i.Commit = i.Commit[:12]
	}
	if dirty && i.Commit != "" {
		i.Commit += "-dirty"
	}
	return i
}

// Version is the short version string ("v0.2.0" or "dev").
func Version() string { return Get().Version }

// String is the one-line `steermi version` output.
func (i Info) String() string {
	s := "steermi " + i.Version
	if i.Commit != "" {
		s += " (" + i.Commit
		if i.Date != "" {
			s += ", " + i.Date
		}
		s += ")"
	}
	return fmt.Sprintf("%s %s %s/%s", s, i.Go, i.OS, i.Arch)
}

// UserAgent identifies the CLI to the Steermi gateway.
func UserAgent() string {
	i := Get()
	return fmt.Sprintf("steermi-cli/%s (%s/%s)", i.Version, i.OS, i.Arch)
}
