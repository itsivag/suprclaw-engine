package configstore

import (
	"errors"
	"os"
	"path/filepath"

	suprclawconfig "github.com/itsivag/suprclaw/pkg/config"
)

const (
	configDirName  = ".suprclaw"
	configFileName = "config.json"
)

func ConfigPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, configFileName), nil
}

func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, configDirName), nil
}

func Load() (*suprclawconfig.Config, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}
	return suprclawconfig.LoadConfig(path)
}

func Save(cfg *suprclawconfig.Config) error {
	if cfg == nil {
		return errors.New("config is nil")
	}
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	return suprclawconfig.SaveConfig(path, cfg)
}
