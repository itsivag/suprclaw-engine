package config

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatVersion_NoGitCommit(t *testing.T) {
	oldVersion, oldGit := Version, GitCommit
	t.Cleanup(func() { Version, GitCommit = oldVersion, oldGit })

	Version = "1.2.3"
	GitCommit = ""

	assert.Equal(t, "1.2.3", FormatVersion())
}

func TestFormatVersion_WithGitCommit(t *testing.T) {
	oldVersion, oldGit := Version, GitCommit
	t.Cleanup(func() { Version, GitCommit = oldVersion, oldGit })

	Version = "1.2.3"
	GitCommit = "abc123"

	assert.Equal(t, "1.2.3 (git: abc123)", FormatVersion())
}

func TestFormatBuildInfo_UsesBuildTimeAndGoVersion_WhenSet(t *testing.T) {
	oldBuildTime, oldGoVersion := BuildTime, GoVersion
	t.Cleanup(func() { BuildTime, GoVersion = oldBuildTime, oldGoVersion })

	BuildTime = "2026-02-20T00:00:00Z"
	GoVersion = "go1.23.0"

	build, goVer := FormatBuildInfo()

	assert.Equal(t, BuildTime, build)
	assert.Equal(t, GoVersion, goVer)
}

func TestFormatBuildInfo_EmptyBuildTime_ReturnsEmptyBuild(t *testing.T) {
	oldBuildTime, oldGoVersion := BuildTime, GoVersion
	t.Cleanup(func() { BuildTime, GoVersion = oldBuildTime, oldGoVersion })

	BuildTime = ""
	GoVersion = "go1.23.0"

	build, goVer := FormatBuildInfo()

	assert.Empty(t, build)
	assert.Equal(t, GoVersion, goVer)
}

func TestFormatBuildInfo_EmptyGoVersion_FallsBackToRuntimeVersion(t *testing.T) {
	oldBuildTime, oldGoVersion := BuildTime, GoVersion
	t.Cleanup(func() { BuildTime, GoVersion = oldBuildTime, oldGoVersion })

	BuildTime = "x"
	GoVersion = ""

	build, goVer := FormatBuildInfo()

	assert.Equal(t, "x", build)
	assert.Equal(t, runtime.Version(), goVer)
}

func TestGetVersion(t *testing.T) {
	oldVersion := Version
	t.Cleanup(func() { Version = oldVersion })

	Version = "dev"
	assert.Equal(t, "dev", GetVersion())
}

func TestGetVersion_Custom(t *testing.T) {
	oldVersion := Version
	t.Cleanup(func() { Version = oldVersion })

	Version = "v1.0.0"
	assert.Equal(t, "v1.0.0", GetVersion())
}

func TestVersion_DefaultIsDev(t *testing.T) {
	// Reset to default values
	oldVersion := Version
	Version = "dev"
	t.Cleanup(func() { Version = oldVersion })

	assert.Equal(t, "dev", Version)
}

func TestParseGHRunCount_Valid(t *testing.T) {
	assert.Equal(t, 123, parseGHRunCount("123"))
}

func TestParseGHRunCount_InvalidOrEmpty(t *testing.T) {
	assert.Equal(t, 0, parseGHRunCount(""))
	assert.Equal(t, 0, parseGHRunCount("abc"))
	assert.Equal(t, 0, parseGHRunCount("0"))
	assert.Equal(t, 0, parseGHRunCount("-12"))
}

func TestGetVersionMetadata_GitHubActionsSource(t *testing.T) {
	oldVersion, oldGit := Version, GitCommit
	oldBuildTime, oldGoVersion := BuildTime, GoVersion
	oldGHRunCount := GHRunCount
	t.Cleanup(func() {
		Version, GitCommit = oldVersion, oldGit
		BuildTime, GoVersion = oldBuildTime, oldGoVersion
		GHRunCount = oldGHRunCount
	})

	Version = "v1.2.3"
	GitCommit = "abc12345"
	BuildTime = "2026-04-04T10:00:00Z"
	GoVersion = "go1.23.0"
	GHRunCount = "456"

	meta := GetVersionMetadata()

	assert.Equal(t, "v1.2.3", meta.Version)
	assert.Equal(t, "abc12345", meta.GitCommit)
	assert.Equal(t, "2026-04-04T10:00:00Z", meta.BuildTime)
	assert.Equal(t, "go1.23.0", meta.GoVersion)
	assert.Equal(t, 456, meta.GHRunCount)
	assert.Equal(t, "github_actions", meta.Source)
}

func TestGetVersionMetadata_LocalSourceOnInvalidRunCount(t *testing.T) {
	oldGHRunCount := GHRunCount
	t.Cleanup(func() { GHRunCount = oldGHRunCount })

	GHRunCount = "not-a-number"
	meta := GetVersionMetadata()

	assert.Equal(t, 0, meta.GHRunCount)
	assert.Equal(t, "local", meta.Source)
}
