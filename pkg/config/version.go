package config

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
)

// Build-time variables injected via ldflags during build process.
// These are set by the Makefile or .goreleaser.yaml using the -X flag:
//
//	-X github.com/itsivag/suprclaw/pkg/config.Version=<version>
//	-X github.com/itsivag/suprclaw/pkg/config.GitCommit=<commit>
//	-X github.com/itsivag/suprclaw/pkg/config.BuildTime=<timestamp>
//	-X github.com/itsivag/suprclaw/pkg/config.GoVersion=<go-version>
//	-X github.com/itsivag/suprclaw/pkg/config.GHRunCount=<github-run-number>
var (
	Version    = "dev" // Default value when not built with ldflags
	GitCommit  string  // Git commit SHA (short)
	BuildTime  string  // Build timestamp in RFC3339 format
	GoVersion  string  // Go version used for building
	GHRunCount string  // GitHub Actions run number when available
)

// VersionMetadata is normalized runtime version/build metadata for APIs.
type VersionMetadata struct {
	Version    string `json:"version"`
	GitCommit  string `json:"git_commit"`
	BuildTime  string `json:"build_time"`
	GoVersion  string `json:"go_version"`
	GHRunCount int    `json:"gh_run_count"`
	Source     string `json:"source"`
}

// FormatVersion returns the version string with optional git commit
func FormatVersion() string {
	v := Version
	if GitCommit != "" {
		v += fmt.Sprintf(" (git: %s)", GitCommit)
	}
	return v
}

// FormatBuildInfo returns build time and go version info
func FormatBuildInfo() (string, string) {
	build := BuildTime
	goVer := GoVersion
	if goVer == "" {
		goVer = runtime.Version()
	}
	return build, goVer
}

// GetVersion returns the version string
func GetVersion() string {
	return Version
}

func parseGHRunCount(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	runCount, err := strconv.Atoi(raw)
	if err != nil || runCount <= 0 {
		return 0
	}
	return runCount
}

// GetVersionMetadata returns normalized version/build metadata for runtime APIs.
func GetVersionMetadata() VersionMetadata {
	build, goVer := FormatBuildInfo()
	runCount := parseGHRunCount(GHRunCount)
	source := "local"
	if runCount > 0 {
		source = "github_actions"
	}

	return VersionMetadata{
		Version:    Version,
		GitCommit:  GitCommit,
		BuildTime:  build,
		GoVersion:  goVer,
		GHRunCount: runCount,
		Source:     source,
	}
}
