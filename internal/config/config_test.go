package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFromHomeDefaultsWhenNoFile(t *testing.T) {
	home := t.TempDir()
	cfg, err := LoadFromHome(home)
	if err != nil {
		t.Fatalf("LoadFromHome() error = %v", err)
	}
	if cfg.Provider != "opencode" {
		t.Fatalf("provider = %q, want opencode", cfg.Provider)
	}
	if cfg.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("schema_version = %d", cfg.SchemaVersion)
	}
}

func TestLoadFromHomeYAMLFirst(t *testing.T) {
	home := t.TempDir()
	confDir := filepath.Join(home, ".zzz")
	if err := os.MkdirAll(confDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "config.yml"), []byte("provider: opencode\ndefault_model: model-x\nmax_consecutive_failures: 4\nui_refresh_ms: 200\nprovider_timeout_seconds: 30\nlock_stale_seconds: 900\nnotes_max_entries: 4\nnotes_char_budget: 2000\nschema_version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFromHome(home)
	if err != nil {
		t.Fatalf("LoadFromHome() error = %v", err)
	}
	if cfg.DefaultModel != "model-x" {
		t.Fatalf("default_model = %q", cfg.DefaultModel)
	}
}

func TestLoadFromHomeErrorsWhenBothFilesExist(t *testing.T) {
	home := t.TempDir()
	confDir := filepath.Join(home, ".zzz")
	if err := os.MkdirAll(confDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "config.yml"), []byte("provider: opencode\ndefault_model: x\nmax_consecutive_failures: 3\nui_refresh_ms: 200\nprovider_timeout_seconds: 30\nlock_stale_seconds: 900\nnotes_max_entries: 4\nnotes_char_budget: 2000\nschema_version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadFromHome(home); err == nil {
		t.Fatal("expected error when both config files exist")
	}
}
