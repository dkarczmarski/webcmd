package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	URLCommands []URLCommand `yaml:"urlCommands"`
}

type URLCommand struct {
	URL             string `yaml:"url"`
	CommandTemplate string `yaml:"commandTemplate"`
	Timeout         int    `yaml:"timeout"`
}

func LoadConfigFromFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	return LoadConfigFromString(string(data))
}

func LoadConfigFromString(content string) (*Config, error) {
	var config Config

	err := yaml.Unmarshal([]byte(content), &config)
	if err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	return &config, nil
}
