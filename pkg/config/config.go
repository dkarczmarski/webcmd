package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config represents the main configuration structure for the application.
type Config struct {
	Server        ServerConfig          `yaml:"server"`
	Authorization []AuthorizationConfig `yaml:"authorization"`
	URLCommands   []URLCommand          `yaml:"urlCommands"`
}

// ServerConfig holds the server-specific configuration settings.
type ServerConfig struct {
	Address string `yaml:"address"`
}

// AuthorizationConfig defines the credentials and permissions for users.
type AuthorizationConfig struct {
	Name string `yaml:"name"`
	Key  string `yaml:"key"`
}

// CommandConfig contains the configuration for a specific command execution.
type CommandConfig struct {
	CommandTemplate string `yaml:"commandTemplate"`
	Timeout         int    `yaml:"timeout"`
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

	return &config, nil
}
