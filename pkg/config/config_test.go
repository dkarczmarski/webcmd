package config_test

import (
	"embed"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dkarczmarski/webcmd/pkg/config"
)

func TestLoadConfig(t *testing.T) {
	t.Parallel()

	configPath := setupTestFile(t, "test-config.yaml")

	t.Run("success loading config", func(t *testing.T) {
		t.Parallel()

		cfg, err := config.LoadConfigFromFile(configPath)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if cfg == nil {
			t.Fatal("expected config to be not nil")
		}

		expected := &config.Config{
			URLCommands: []config.URLCommand{
				{
					URL: "POST /cmd/echo",
					CommandConfig: config.CommandConfig{
						CommandTemplate: "/bin/echo\n{{.param1}}\n{{.param2}}\n",
						Timeout:         5,
					},
				},
				{
					URL: "POST /cmd/sleep",
					CommandConfig: config.CommandConfig{
						CommandTemplate: "/usr/bin/sleep\n20\n",
						Timeout:         30,
					},
				},
			},
		}

		if !reflect.DeepEqual(cfg, expected) {
			t.Errorf("config mismatch\nexpected: %+v\ngot: %+v", expected, cfg)
		}
	})

	t.Run("file not found", func(t *testing.T) {
		t.Parallel()

		_, err := config.LoadConfigFromFile("non-existent-file.yaml")
		if err == nil {
			t.Fatal("expected error for non-existent file, got nil")
		}
	})
}

//go:embed test-data/test-config.yaml
var testFiles embed.FS

func setupTestFile(t *testing.T, fileName string) string {
	t.Helper()

	// Read embedded file
	data, err := testFiles.ReadFile(filepath.Join("test-data", fileName))
	if err != nil {
		t.Fatalf("failed to read embedded file: %v", err)
	}

	// Create temporary directory for test file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, fileName)

	// Write embedded data to temporary file
	err = os.WriteFile(configPath, data, 0o600)
	if err != nil {
		t.Fatalf("failed to write temporary config file: %v", err)
	}

	return configPath
}
