package bailiff

import (
	"errors"
	"fmt"
	"os"
	"regexp"

	"github.com/go-yaml/yaml"
)

const (
	DefaultGreetingTemplate = "Hello! Thanks for your contribution. Someone on our team will be with you shortly to review your changes and authorize them to run in CI.\n\nAdditional changes will need to be authorized again."
)

type Config struct {
	ListenAddr        string         `yaml:"listen_addr"`
	AdminTeams        []string       `yaml:"admin_teams"`
	Org               string         `yaml:"org"`
	Repo              string         `yaml:"repo"`
	TriggerPatternStr string         `yaml:"trigger_pattern"`
	TriggerPattern    *regexp.Regexp `yaml:"-"`
	GreetingTemplate  string         `yaml:"greeting_template"`
	StatusName        string         `yaml:"status_name"`
}

func (c *Config) Check() error {
	if c.ListenAddr == "" {
		c.ListenAddr = "0.0.0.0:8080"
	}

	if len(c.AdminTeams) == 0 {
		return errors.New("must define at least one admin team")
	}

	if c.Org == "" {
		return errors.New("must define the organization")
	}

	if c.Repo == "" {
		return errors.New("must define the repository")
	}

	if c.TriggerPatternStr == "" {
		c.TriggerPatternStr = `(?m)^/ci authorize (?P<sha>[a-f0-9]+)$`
	}

	pat, err := regexp.Compile(c.TriggerPatternStr)
	if err != nil {
		return fmt.Errorf("error compiling trigger pattern: %w", err)
	}
	c.TriggerPattern = pat

	if c.GreetingTemplate == "" {
		c.GreetingTemplate = DefaultGreetingTemplate
	}

	if c.StatusName == "" {
		c.StatusName = "bailiff"
	}

	return nil
}

type EnvConfig struct {
	WebhookSecret  string
	GitHubToken    string
	PrivateKeyFile string
}

func (e EnvConfig) Check() error {
	if e.WebhookSecret == "" {
		return errors.New("must define the webhook secret")
	}

	if e.GitHubToken == "" {
		return errors.New("must define the GitHub token")
	}

	if e.PrivateKeyFile == "" {
		return errors.New("must define the private key file")
	}

	return nil
}

func ReadConfig(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("error opening config file: %w", err)
	}
	defer f.Close()

	var cfg Config
	if err := yaml.NewDecoder(f).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("error decoding config file: %w", err)
	}

	return &cfg, nil
}
