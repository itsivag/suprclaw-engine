package config

import (
	"os"
	"path/filepath"
	"strings"
)

// ResolveSuprclawHome returns the suprclaw home directory.
// Priority: $SUPRCLAW_HOME > ~/.suprclaw
func ResolveSuprclawHome() string {
	if home := strings.TrimSpace(os.Getenv("SUPRCLAW_HOME")); home != "" {
		return filepath.Clean(expandUserHome(home))
	}

	userHome, err := os.UserHomeDir()
	if err != nil {
		return filepath.Clean(".suprclaw")
	}
	return filepath.Join(userHome, ".suprclaw")
}

// ResolveGlobalSkillsDir returns the shared global skills directory.
//
// Resolution rules:
// 1. If configured is empty, default to <suprclaw_home>/skills.
// 2. Trim whitespace.
// 3. Expand "~/" to user home.
// 4. If relative, resolve against <suprclaw_home>.
// 5. Return a cleaned absolute path when possible.
func ResolveGlobalSkillsDir(configured string) string {
	base := ResolveSuprclawHome()
	raw := strings.TrimSpace(configured)
	if raw == "" {
		raw = filepath.Join(base, "skills")
	}

	raw = expandUserHome(raw)
	if !filepath.IsAbs(raw) {
		raw = filepath.Join(base, raw)
	}

	clean := filepath.Clean(raw)
	if abs, err := filepath.Abs(clean); err == nil {
		return abs
	}
	return clean
}

func expandUserHome(p string) string {
	if !strings.HasPrefix(p, "~") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}

	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}
