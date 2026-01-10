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

		configuration := mustLoadConfig(t, configPath)

		if configuration == nil {
			t.Fatal("expected config to be not nil")
		}

		expected := &config.Config{
			Server: config.ServerConfig{
				Address: ":8080",
				HTTPSConfig: config.ServerHTTPSConfig{
					Enabled:  true,
					CertFile: "/etc/certs/cert.pem",
					KeyFile:  "/etc/certs/key.pem",
				},
			},
			Authorization: []config.AuthorizationConfig{
				{
					Name: "auth-name1",
					Key:  "ABC111",
				},
				{
					Name: "auth-name2",
					Key:  "ABC222",
				},
			},
			URLCommands: []config.URLCommand{
				{
					URL:               "POST /cmd/echo",
					AuthorizationName: "auth-name1,auth-name2",
					CommandConfig: config.CommandConfig{
						CommandTemplate: "/bin/echo\n{{.param1}}\n{{.param2}}\n",
						Params: config.ParamsConfig{
							BodyAsText: ptrBool(true),
							BodyAsJSON: ptrBool(true),
						},
						Timeout:    5,
						OutputType: "text",
					},
				},
				{
					URL:               "POST /cmd/sleep",
					AuthorizationName: "",
					CommandConfig: config.CommandConfig{
						CommandTemplate: "/usr/bin/sleep\n20\n",
						Params: config.ParamsConfig{
							BodyAsText: ptrBool(true),
							BodyAsJSON: ptrBool(false),
						},
						Timeout: 30,
					},
				},
			},
		}

		if !reflect.DeepEqual(configuration, expected) {
			t.Errorf("config mismatch\nexpected: %+v\ngot: %+v", expected, configuration)
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

//go:embed test-data/*.yaml
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

func ptrBool(b bool) *bool {
	return &b
}

func TestSetDefaults(t *testing.T) {
	t.Parallel()

	t.Run("HTTPS disabled default address", func(t *testing.T) {
		t.Parallel()

		configPath := setupTestFile(t, "https-disabled.yaml")

		cfg := mustLoadConfig(t, configPath)

		config.SetDefaults(cfg)

		expected := "127.0.0.1:8080"

		if cfg.Server.Address != expected {
			t.Errorf("expected address %s, got %s", expected, cfg.Server.Address)
		}
	})

	t.Run("HTTPS enabled default address", func(t *testing.T) {
		t.Parallel()

		configPath := setupTestFile(t, "https-enabled.yaml")

		cfg := mustLoadConfig(t, configPath)

		config.SetDefaults(cfg)

		expected := "127.0.0.1:8443"

		if cfg.Server.Address != expected {
			t.Errorf("expected address %s, got %s", expected, cfg.Server.Address)
		}
	})

	t.Run("Existing address not overwritten", func(t *testing.T) {
		t.Parallel()

		configPath := setupTestFile(t, "custom-address.yaml")

		cfg := mustLoadConfig(t, configPath)

		config.SetDefaults(cfg)

		expected := "0.0.0.0:9000"

		if cfg.Server.Address != expected {
			t.Errorf("expected address %s, got %s", expected, cfg.Server.Address)
		}
	})

	t.Run("Params default values", func(t *testing.T) {
		t.Parallel()

		configPath := setupTestFile(t, "params-default.yaml")
		cfg := mustLoadConfig(t, configPath)

		if len(cfg.URLCommands) != 1 {
			t.Fatal("expected 1 URL command")
		}

		params := cfg.URLCommands[0].Params

		if !config.IsTrue(params.BodyAsText) {
			t.Errorf("expected BodyAsText to be true by default, got %v", params.BodyAsText)
		}

		if config.IsTrue(params.BodyAsJSON) {
			t.Errorf("expected BodyAsJSON to be false by default, got %v", params.BodyAsJSON)
		}
	})

	t.Run("Params override values", func(t *testing.T) {
		t.Parallel()

		configPath := setupTestFile(t, "params-override.yaml")
		cfg := mustLoadConfig(t, configPath)

		params := cfg.URLCommands[0].Params

		if config.IsTrue(params.BodyAsText) {
			t.Errorf("expected BodyAsText to be false, got %v", params.BodyAsText)
		}

		if !config.IsTrue(params.BodyAsJSON) {
			t.Errorf("expected BodyAsJSON to be true, got %v", params.BodyAsJSON)
		}
	})

	t.Run("Params mixed values", func(t *testing.T) {
		t.Parallel()

		configPath := setupTestFile(t, "params-mixed.yaml")
		cfg := mustLoadConfig(t, configPath)

		params := cfg.URLCommands[0].Params

		if !config.IsTrue(params.BodyAsText) {
			t.Errorf("expected BodyAsText to be true (default), got %v", params.BodyAsText)
		}

		if !config.IsTrue(params.BodyAsJSON) {
			t.Errorf("expected BodyAsJSON to be true (overridden), got %v", params.BodyAsJSON)
		}
	})
}

func mustLoadConfig(t *testing.T, configPath string) *config.Config {
	t.Helper()

	configuration, err := config.LoadConfigFromFile(configPath)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	return configuration
}
