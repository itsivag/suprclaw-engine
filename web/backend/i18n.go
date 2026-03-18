package main

import (
	"fmt"
	"os"
)

// Language represents the supported languages
type Language string

const (
	LanguageEnglish Language = "en"
)

// current language (default: English)
var currentLang Language = LanguageEnglish

// TranslationKey represents a translation key used for i18n
type TranslationKey string

const (
	AppTooltip         TranslationKey = "AppTooltip"
	MenuOpen           TranslationKey = "MenuOpen"
	MenuOpenTooltip    TranslationKey = "MenuOpenTooltip"
	MenuAbout          TranslationKey = "MenuAbout"
	MenuAboutTooltip   TranslationKey = "MenuAboutTooltip"
	MenuVersion        TranslationKey = "MenuVersion"
	MenuVersionTooltip TranslationKey = "MenuVersionTooltip"
	MenuGitHub         TranslationKey = "MenuGitHub"
	MenuDocs           TranslationKey = "MenuDocs"
	MenuRestart        TranslationKey = "MenuRestart"
	MenuRestartTooltip TranslationKey = "MenuRestartTooltip"
	MenuQuit           TranslationKey = "MenuQuit"
	MenuQuitTooltip    TranslationKey = "MenuQuitTooltip"
	Exiting            TranslationKey = "Exiting"
	DocUrl             TranslationKey = "DocUrl"
)

// Translation tables
var translations = map[Language]map[TranslationKey]string{
	LanguageEnglish: {
		AppTooltip:         "%s - Web Console",
		MenuOpen:           "Open Console",
		MenuOpenTooltip:    "Open SuprClaw console in browser",
		MenuAbout:          "About",
		MenuAboutTooltip:   "About SuprClaw",
		MenuVersion:        "Version: %s",
		MenuVersionTooltip: "Current version number",
		MenuGitHub:         "GitHub",
		MenuDocs:           "Documentation",
		MenuRestart:        "Restart Service",
		MenuRestartTooltip: "Restart Gateway service",
		MenuQuit:           "Quit",
		MenuQuitTooltip:    "Exit SuprClaw",
		Exiting:            "Exiting SuprClaw...",
		DocUrl:             "https://docs.suprclaw.io/docs/",
	},
}

// SetLanguage sets the current language
func SetLanguage(lang string) {
	_ = lang
	currentLang = LanguageEnglish
}

// GetLanguage returns the current language
func GetLanguage() Language {
	return currentLang
}

// T translates a key to the current language
func T(key TranslationKey, args ...any) string {
	if trans, ok := translations[currentLang][key]; ok {
		if len(args) > 0 {
			return fmt.Sprintf(trans, args...)
		}
		return trans
	}
	return string(key)
}

// Initialize i18n from environment variable
func init() {
	if lang := os.Getenv("LANG"); lang != "" {
		SetLanguage(lang)
	}
}
