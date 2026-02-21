package config_test

import (
	"embed"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

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
				Address:             ":8080",
				ShutdownGracePeriod: ptrDuration(5 * time.Second),
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
							BodyAsJSON: ptrBool(true),
						},
						Timeout:    ptrDuration(5 * time.Second),
						OutputType: "text",
					},
				},
				{
					URL:               "POST /cmd/sleep",
					AuthorizationName: "",
					CommandConfig: config.CommandConfig{
						CommandTemplate: "/usr/bin/sleep\n20\n",
						Params: config.ParamsConfig{
							BodyAsJSON: ptrBool(false),
						},
						Timeout: ptrDuration(30 * time.Second),
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

		if config.IsTrue(params.BodyAsJSON) {
			t.Errorf("expected BodyAsJSON to be false by default, got %v", params.BodyAsJSON)
		}
	})

	t.Run("Params override values", func(t *testing.T) {
		t.Parallel()

		configPath := setupTestFile(t, "params-override.yaml")
		cfg := mustLoadConfig(t, configPath)

		params := cfg.URLCommands[0].Params

		if !config.IsTrue(params.BodyAsJSON) {
			t.Errorf("expected BodyAsJSON to be true, got %v", params.BodyAsJSON)
		}
	})

	t.Run("ShutdownGracePeriod default value", func(t *testing.T) {
		t.Parallel()

		configPath := setupTestFile(t, "https-disabled.yaml")
		cfg := mustLoadConfig(t, configPath)

		if cfg.Server.ShutdownGracePeriod == nil {
			t.Fatal("expected ShutdownGracePeriod to be set by default")
		}

		if *cfg.Server.ShutdownGracePeriod != 5*time.Second {
			t.Errorf("expected ShutdownGracePeriod 5s, got %v", *cfg.Server.ShutdownGracePeriod)
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

func TestGraceTerminationTimeout(t *testing.T) {
	t.Parallel()

	yamlContent := `
urlCommands:
  - url: POST /test
    commandTemplate: /usr/bin/echo
    graceTerminationTimeout: 10s
`

	cfg, err := config.LoadConfigFromString(yamlContent)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if len(cfg.URLCommands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(cfg.URLCommands))
	}

	graceTimeout := cfg.URLCommands[0].GraceTerminationTimeout
	if graceTimeout == nil {
		t.Fatal("expected GraceTerminationTimeout to be set")
	}

	expected := 10 * time.Second
	if *graceTimeout != expected {
		t.Errorf("expected GraceTerminationTimeout %v, got %v", expected, *graceTimeout)
	}
}

func TestCallGateConfig(t *testing.T) {
	t.Parallel()

	yamlConfig := `
urlCommands:
  - url: POST /cmd/echo
    commandTemplate: /bin/echo
    callGate:
      mode: single
      groupName: myGroupName1
  - url: POST /cmd/seq
    commandTemplate: /bin/ls
    callGate:
      mode: sequence
      groupName: myGroupName2
`

	configuration, err := config.LoadConfigFromString(yamlConfig)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if len(configuration.URLCommands) != 2 {
		t.Fatalf("expected 2 URLCommands, got %d", len(configuration.URLCommands))
	}

	// First command
	cmd1 := configuration.URLCommands[0]

	if cmd1.CallGate == nil {
		t.Fatal("expected CallGate to be not nil for first command")
	}

	if cmd1.CallGate.Mode != "single" {
		t.Errorf("expected mode single, got %s", cmd1.CallGate.Mode)
	}

	if cmd1.CallGate.GroupName == nil || *cmd1.CallGate.GroupName != "myGroupName1" {
		t.Errorf("expected groupName myGroupName1, got %v", cmd1.CallGate.GroupName)
	}

	// Second command
	cmd2 := configuration.URLCommands[1]

	if cmd2.CallGate == nil {
		t.Fatal("expected CallGate to be not nil for second command")
	}

	if cmd2.CallGate.Mode != "sequence" {
		t.Errorf("expected mode sequence, got %s", cmd2.CallGate.Mode)
	}

	if cmd2.CallGate.GroupName == nil || *cmd2.CallGate.GroupName != "myGroupName2" {
		t.Errorf("expected groupName myGroupName2, got %v", cmd2.CallGate.GroupName)
	}
}

func TestCallGateConfig_OptionalGroupName(t *testing.T) {
	t.Parallel()

	yamlConfig := `
urlCommands:
  - url: POST /cmd/no-group
    commandTemplate: /bin/echo
    callGate:
      mode: single
  - url: POST /cmd/empty-group
    commandTemplate: /bin/echo
    callGate:
      mode: single
      groupName: ""
`

	cfg, err := config.LoadConfigFromString(yamlConfig)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if len(cfg.URLCommands) != 2 {
		t.Fatalf("expected 2 URLCommands, got %d", len(cfg.URLCommands))
	}

	// Case 1: missing groupName -> GroupName should be nil
	cmd1 := cfg.URLCommands[0]
	if cmd1.CallGate.GroupName != nil {
		t.Errorf("expected GroupName to be nil for missing value, got %v", *cmd1.CallGate.GroupName)
	}

	// Case 2: empty groupName -> GroupName should be non-nil and empty string
	cmd2 := cfg.URLCommands[1]

	if cmd2.CallGate.GroupName == nil {
		t.Fatal("expected GroupName to be not nil for empty string value")
	}

	if *cmd2.CallGate.GroupName != "" {
		t.Errorf("expected GroupName to be empty string, got %q", *cmd2.CallGate.GroupName)
	}
}

func ptrBool(b bool) *bool {
	return &b
}

func ptrDuration(d time.Duration) *time.Duration {
	return &d
}
