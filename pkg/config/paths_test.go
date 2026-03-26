package config

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveSuprclawHome_Default(t *testing.T) {
	t.Setenv("SUPRCLAW_HOME", "")

	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", `C:\tmp\home`)
		want := filepath.Clean(filepath.Join(`C:\tmp\home`, ".suprclaw"))
		if got := ResolveSuprclawHome(); got != want {
			t.Fatalf("ResolveSuprclawHome() = %q, want %q", got, want)
		}
		return
	}

	t.Setenv("HOME", "/tmp/home")
	want := filepath.Clean("/tmp/home/.suprclaw")
	if got := ResolveSuprclawHome(); got != want {
		t.Fatalf("ResolveSuprclawHome() = %q, want %q", got, want)
	}
}

func TestResolveSuprclawHome_FromEnv(t *testing.T) {
	t.Setenv("SUPRCLAW_HOME", " /custom/suprclaw/home ")

	want := filepath.Clean("/custom/suprclaw/home")
	if got := ResolveSuprclawHome(); got != want {
		t.Fatalf("ResolveSuprclawHome() = %q, want %q", got, want)
	}
}

func TestResolveGlobalSkillsDir_Default(t *testing.T) {
	t.Setenv("SUPRCLAW_HOME", "/custom/suprclaw/home")

	want := filepath.Clean("/custom/suprclaw/home/skills")
	if got := ResolveGlobalSkillsDir(""); got != want {
		t.Fatalf("ResolveGlobalSkillsDir(\"\") = %q, want %q", got, want)
	}
}

func TestResolveGlobalSkillsDir_Relative(t *testing.T) {
	t.Setenv("SUPRCLAW_HOME", "/custom/suprclaw/home")

	want := filepath.Clean("/custom/suprclaw/home/shared/skills")
	if got := ResolveGlobalSkillsDir(" shared/skills "); got != want {
		t.Fatalf("ResolveGlobalSkillsDir(relative) = %q, want %q", got, want)
	}
}

func TestResolveGlobalSkillsDir_Tilde(t *testing.T) {
	t.Setenv("SUPRCLAW_HOME", "")

	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", `C:\Users\Test`)
		want := filepath.Clean(filepath.Join(`C:\Users\Test`, "global", "skills"))
		if got := ResolveGlobalSkillsDir("~/global/skills"); got != want {
			t.Fatalf("ResolveGlobalSkillsDir(tilde) = %q, want %q", got, want)
		}
		return
	}

	t.Setenv("HOME", "/tmp/home")
	want := filepath.Clean("/tmp/home/global/skills")
	if got := ResolveGlobalSkillsDir("~/global/skills"); got != want {
		t.Fatalf("ResolveGlobalSkillsDir(tilde) = %q, want %q", got, want)
	}
}
