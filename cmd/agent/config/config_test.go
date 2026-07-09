package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfigFile(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	return path
}

func TestLoadFileAppliesIntervalOverride(t *testing.T) {
	t.Parallel()

	cfg, err := Load(writeConfigFile(t, "interval: 9\n"))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Interval != 9 {
		t.Fatalf("cfg.Interval = %d, want 9", cfg.Interval)
	}
}

func TestLoadReturnsDefaultsWhenPathIsEmpty(t *testing.T) {
	t.Parallel()

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Interval != DefaultInterval {
		t.Fatalf("cfg.Interval = %d, want %d", cfg.Interval, DefaultInterval)
	}
}

func TestApplyDefaultsRepairsInvalidInterval(t *testing.T) {
	t.Parallel()

	cfg := Agent{}
	cfg.ApplyDefaults()

	if cfg.Interval != DefaultInterval {
		t.Fatalf("cfg.Interval = %d, want %d", cfg.Interval, DefaultInterval)
	}
}
