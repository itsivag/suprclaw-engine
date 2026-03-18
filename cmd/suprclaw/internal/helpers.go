package internal

import (
	"os"
	"path/filepath"

	"github.com/itsivag/suprclaw/pkg/config"
)

const Logo = "🦞"

// GetSuprclawHome returns the suprclaw home directory.
// Priority: $SUPRCLAW_HOME > ~/.suprclaw
func GetSuprclawHome() string {
	if home := os.Getenv("SUPRCLAW_HOME"); home != "" {
		return home
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".suprclaw")
}

func GetConfigPath() string {
	if configPath := os.Getenv("SUPRCLAW_CONFIG"); configPath != "" {
		return configPath
	}
	return filepath.Join(GetSuprclawHome(), "config.json")
}

func LoadConfig() (*config.Config, error) {
	return config.LoadConfig(GetConfigPath())
}

// FormatVersion returns the version string with optional git commit
// Deprecated: Use pkg/config.FormatVersion instead
func FormatVersion() string {
	return config.FormatVersion()
}

// FormatBuildInfo returns build time and go version info
// Deprecated: Use pkg/config.FormatBuildInfo instead
func FormatBuildInfo() (string, string) {
	return config.FormatBuildInfo()
}

// GetVersion returns the version string
// Deprecated: Use pkg/config.GetVersion instead
func GetVersion() string {
	return config.GetVersion()
}
