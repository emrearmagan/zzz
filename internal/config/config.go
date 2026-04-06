package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const CurrentSchemaVersion = 1

type RunConfig struct {
	Provider               string `json:"provider" yaml:"provider"`
	DefaultModel           string `json:"default_model" yaml:"default_model"`
	MaxIterations          int    `json:"max_iterations" yaml:"max_iterations"`
	MaxConsecutiveFailures int    `json:"max_consecutive_failures" yaml:"max_consecutive_failures"`
	UIRefreshMS            int    `json:"ui_refresh_ms" yaml:"ui_refresh_ms"`
	ProviderTimeoutSeconds int    `json:"provider_timeout_seconds" yaml:"provider_timeout_seconds"`
	LockStaleSeconds       int    `json:"lock_stale_seconds" yaml:"lock_stale_seconds"`
	NotesMaxEntries        int    `json:"notes_max_entries" yaml:"notes_max_entries"`
	NotesCharBudget        int    `json:"notes_char_budget" yaml:"notes_char_budget"`
	SchemaVersion          int    `json:"schema_version" yaml:"schema_version"`
}

func Default() RunConfig {
	return RunConfig{
		Provider:               "opencode",
		DefaultModel:           "opencode/minimax-m.2.5-free",
		MaxIterations:          5,
		MaxConsecutiveFailures: 3,
		UIRefreshMS:            200,
		ProviderTimeoutSeconds: 600,
		LockStaleSeconds:       900,
		NotesMaxEntries:        20,
		NotesCharBudget:        12000,
		SchemaVersion:          CurrentSchemaVersion,
	}
}

func Load() (RunConfig, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return RunConfig{}, fmt.Errorf("resolve home directory: %w", err)
	}
	return LoadFromHome(home)
}

func LoadFromHome(home string) (RunConfig, error) {
	base := filepath.Join(home, ".zzz")
	yamlPath := filepath.Join(base, "config.yml")
	jsonPath := filepath.Join(base, "config.json")

	yamlExists := fileExists(yamlPath)
	jsonExists := fileExists(jsonPath)
	if yamlExists && jsonExists {
		return RunConfig{}, errors.New("both ~/.zzz/config.yml and ~/.zzz/config.json exist; remove one to continue")
	}

	cfg := Default()
	if yamlExists {
		if err := decodeYAML(yamlPath, &cfg); err != nil {
			return RunConfig{}, fmt.Errorf("load %s: %w", yamlPath, err)
		}
	} else if jsonExists {
		if err := decodeJSON(jsonPath, &cfg); err != nil {
			return RunConfig{}, fmt.Errorf("load %s: %w", jsonPath, err)
		}
	}

	if err := Validate(cfg); err != nil {
		return RunConfig{}, err
	}
	return cfg, nil
}

func Validate(cfg RunConfig) error {
	if cfg.Provider == "" {
		return errors.New("provider is required")
	}
	if cfg.DefaultModel == "" {
		return errors.New("default_model is required")
	}
	if cfg.MaxIterations < 0 {
		return errors.New("max_iterations must be >= 0")
	}
	if cfg.MaxConsecutiveFailures < 1 {
		return errors.New("max_consecutive_failures must be >= 1")
	}
	if cfg.UIRefreshMS < 50 {
		return errors.New("ui_refresh_ms must be >= 50")
	}
	if cfg.ProviderTimeoutSeconds < 1 {
		return errors.New("provider_timeout_seconds must be >= 1")
	}
	if cfg.LockStaleSeconds < 60 {
		return errors.New("lock_stale_seconds must be >= 60")
	}
	if cfg.NotesMaxEntries < 1 {
		return errors.New("notes_max_entries must be >= 1")
	}
	if cfg.NotesCharBudget < 500 {
		return errors.New("notes_char_budget must be >= 500")
	}
	if cfg.SchemaVersion != CurrentSchemaVersion {
		return fmt.Errorf("unsupported schema_version %d (expected %d)", cfg.SchemaVersion, CurrentSchemaVersion)
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func decodeYAML(path string, out *RunConfig) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(b, out)
}

func decodeJSON(path string, out *RunConfig) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}
