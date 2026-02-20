package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	DefaultHTTPAddress         = "127.0.0.1:8080"
	DefaultHTTPSAddress        = "127.0.0.1:8443"
	DefaultShutdownGracePeriod = 5 * time.Second
	DefaultBodyAsJSON          = false
)

// Config represents the main configuration structure for the application.
type Config struct {
	Server        ServerConfig          `yaml:"server"`
	Authorization []AuthorizationConfig `yaml:"authorization"`
	URLCommands   []URLCommand          `yaml:"urlCommands"`
}

// ServerConfig holds the server-specific configuration settings.
type ServerConfig struct {
	Address             string            `yaml:"address"`
	ShutdownGracePeriod *time.Duration    `yaml:"shutdownGracePeriod"`
	HTTPSConfig         ServerHTTPSConfig `yaml:"https"`
}

// ServerHTTPSConfig contains the configuration for the HTTPS server.
type ServerHTTPSConfig struct {
	// Enabled specifies whether HTTPS is enabled.
	Enabled bool `yaml:"enabled"`
	// CertFile is the path to the SSL certificate file.
	CertFile string `yaml:"certFile"`
	// KeyFile is the path to the SSL key file.
	KeyFile string `yaml:"keyFile"`
}

// AuthorizationConfig defines the credentials and permissions for users.
type AuthorizationConfig struct {
	Name string `yaml:"name"`
	Key  string `yaml:"key"`
}

// ParamsConfig contains optional configuration for request body processing.
type ParamsConfig struct {
	BodyAsJSON *bool `yaml:"bodyAsJson"`
}

// CallGateConfig contains the configuration for the call gate.
type CallGateConfig struct {
	Mode      string `yaml:"mode"`
	GroupName string `yaml:"groupName"`
}

// CommandConfig contains the configuration for a specific command execution.
type CommandConfig struct {
	CommandTemplate         string          `yaml:"commandTemplate"`
	Params                  ParamsConfig    `yaml:"params"`
	Timeout                 *time.Duration  `yaml:"timeout"`
	GraceTerminationTimeout *time.Duration  `yaml:"graceTerminationTimeout"`
	OutputType              string          `yaml:"outputType"`
	CallGate                *CallGateConfig `yaml:"callGate"`
}

// URLCommand maps an HTTP request (method and path) to a command configuration.
type URLCommand struct {
	URL               string `yaml:"url"`
	AuthorizationName string `yaml:"authorizationName"`
	CommandConfig     `yaml:",inline"`
}

// LoadConfigFromFile reads the configuration from a YAML file at the specified path.
func LoadConfigFromFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	return LoadConfigFromString(string(data))
}

// LoadConfigFromString parses the configuration from a YAML string.
func LoadConfigFromString(content string) (*Config, error) {
	var config Config

	err := yaml.Unmarshal([]byte(content), &config)
	if err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	SetDefaults(&config)

	return &config, nil
}

// SetDefaults sets default values for the configuration if they are not provided.
func SetDefaults(config *Config) {
	if config.Server.Address == "" {
		if config.Server.HTTPSConfig.Enabled {
			config.Server.Address = DefaultHTTPSAddress
		} else {
			config.Server.Address = DefaultHTTPAddress
		}
	}

	if config.Server.ShutdownGracePeriod == nil {
		d := DefaultShutdownGracePeriod
		config.Server.ShutdownGracePeriod = &d
	}

	for i := range config.URLCommands {
		setBodyAsJSONDefault(&config.URLCommands[i].Params)
	}
}

// IsTrue returns true if b is not nil and its value is true.
func IsTrue(b *bool) bool {
	return b != nil && *b
}

func setBodyAsJSONDefault(params *ParamsConfig) {
	if params.BodyAsJSON == nil {
		val := DefaultBodyAsJSON
		params.BodyAsJSON = &val
	}
}
