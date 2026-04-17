package gogmoon

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const DefaultConfigPath = "gog-config.yml"

type Config struct {
	ListenAddr string    `yaml:"listen_addr"`
	Gog        GogConfig `yaml:"gog"`
}

type GogConfig struct {
	Binary         string        `yaml:"binary"`
	Account        string        `yaml:"account"`
	Subject        string        `yaml:"subject"`
	FetchInterval  time.Duration `yaml:"fetch_interval"`
	CommandTimeout time.Duration `yaml:"command_timeout"`
	Workers        int           `yaml:"workers"`
	MaxResults     int           `yaml:"max_results"`
	TempPattern    string        `yaml:"temp_pattern"`
	UnreadOnly     bool          `yaml:"unread_only"`
	SearchSubject  bool          `yaml:"search_subject"`
	SearchQueries  []string      `yaml:"search_queries"`
}

type ServiceConfig struct {
	FetchInterval  time.Duration
	CommandTimeout time.Duration
	Workers        int
	QueueSize      int
}

func DefaultConfig() Config {
	return Config{
		ListenAddr: ":8081",
		Gog: GogConfig{
			Binary:         "gog",
			Account:        "me",
			Subject:        "moon-shell",
			FetchInterval:  5 * time.Second,
			CommandTimeout: time.Minute,
			Workers:        1,
			MaxResults:     50,
			TempPattern:    "moon-shell-gog-*",
			UnreadOnly:     true,
			SearchSubject:  true,
			SearchQueries: []string{
				"in:inbox",
				"in:spam",
			},
		},
	}
}

func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	if path == "" {
		return cfg, nil
	}

	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if path == DefaultConfigPath {
				return cfg, nil
			}
			return Config{}, fmt.Errorf("open config %q: %w", path, err)
		}
		return Config{}, fmt.Errorf("open config %q: %w", path, err)
	}
	defer file.Close()

	if err := decodeConfigYAML(file, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode config %q: %w", path, err)
	}

	return cfg, nil
}

func (c Config) Validate() error {
	var problems []string
	if c.ListenAddr == "" {
		problems = append(problems, "listen_addr must not be empty")
	}
	if c.Gog.Binary == "" {
		problems = append(problems, "gog.binary must not be empty")
	}
	if c.Gog.Account == "" {
		problems = append(problems, "gog.account must not be empty")
	}
	if c.Gog.Subject == "" {
		problems = append(problems, "gog.subject must not be empty")
	}
	if c.Gog.FetchInterval <= 0 {
		problems = append(problems, "gog.fetch_interval must be greater than zero")
	}
	if c.Gog.CommandTimeout <= 0 {
		problems = append(problems, "gog.command_timeout must be greater than zero")
	}
	if c.Gog.Workers <= 0 {
		problems = append(problems, "gog.workers must be greater than zero")
	}
	if c.Gog.MaxResults <= 0 {
		problems = append(problems, "gog.max_results must be greater than zero")
	}
	if len(c.Gog.SearchQueries) == 0 {
		problems = append(problems, "gog.search_queries must contain at least one query")
	}

	if len(problems) == 0 {
		return nil
	}
	return errors.New(strings.Join(problems, "; "))
}

func (c Config) ServiceConfig() ServiceConfig {
	workers := c.Gog.Workers
	if workers <= 0 {
		workers = 1
	}
	queueSize := workers * c.Gog.MaxResults * len(c.Gog.SearchQueries)
	if queueSize < workers*2 {
		queueSize = workers * 2
	}
	return ServiceConfig{
		FetchInterval:  c.Gog.FetchInterval,
		CommandTimeout: c.Gog.CommandTimeout,
		Workers:        workers,
		QueueSize:      queueSize,
	}
}

func (c ServiceConfig) GetCommandTimeout() time.Duration {
	return c.CommandTimeout
}

func decodeConfigYAML(r io.Reader, out *Config) error {
	decoder := yaml.NewDecoder(r)
	decoder.KnownFields(true)
	err := decoder.Decode(out)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}
