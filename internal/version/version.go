// Package version exposes build-time identity for every TEO binary.
// Values are populated via -ldflags by the Makefile and the release pipeline.
package version

import (
	"fmt"
	"runtime"
)

// These are overridden at build time. Defaults make a `go run` work without ldflags.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// Info bundles build-time identity.
type Info struct {
	Service   string `json:"service"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	GoVersion string `json:"goVersion"`
	Platform  string `json:"platform"`
}

// Get returns Info for the calling service.
func Get(service string) Info {
	return Info{
		Service:   service,
		Version:   Version,
		Commit:    Commit,
		Date:      Date,
		GoVersion: runtime.Version(),
		Platform:  fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	}
}

// String formats Info as a single human-readable line.
func (i Info) String() string {
	return fmt.Sprintf("%s %s (commit=%s date=%s %s %s)",
		i.Service, i.Version, i.Commit, i.Date, i.GoVersion, i.Platform)
}
