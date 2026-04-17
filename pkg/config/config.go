package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	gmailconfig "moon-shell/pkg/gmail/config"
)

const DefaultPath = "config.yml"

type Config struct {
	ListenAddr string             `yaml:"listen_addr"`
	Gmail      gmailconfig.Config `yaml:"gmail"`
}

func Default() Config {
	return Config{
		ListenAddr: ":8080",
		Gmail: gmailconfig.Config{
			User:           "me",
			Subject:        "moon-shell",
			FetchInterval:  time.Minute,
			CommandTimeout: time.Minute,
			Workers:        2,
			MaxResults:     25,
			Watch: gmailconfig.WatchConfig{
				RenewBefore: time.Hour,
			},
		},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}

	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if path == DefaultPath {
				return cfg, nil
			}
			return Config{}, fmt.Errorf("open config %q: %w", path, err)
		}
		return Config{}, fmt.Errorf("open config %q: %w", path, err)
	}
	defer file.Close()

	if err := decodeYAML(file, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode config %q: %w", path, err)
	}

	return cfg, nil
}

func (c Config) Validate() error {
	var problems []string

	if c.ListenAddr == "" {
		problems = append(problems, "listen_addr must not be empty")
	}
	if c.Gmail.User == "" {
		problems = append(problems, "gmail.user must not be empty")
	}
	if c.Gmail.Subject == "" {
		problems = append(problems, "gmail.subject must not be empty")
	}
	if c.Gmail.FetchInterval <= 0 {
		problems = append(problems, "gmail.fetch_interval must be greater than zero")
	}
	if c.Gmail.CommandTimeout <= 0 {
		problems = append(problems, "gmail.command_timeout must be greater than zero")
	}
	if c.Gmail.Workers <= 0 {
		problems = append(problems, "gmail.workers must be greater than zero")
	}
	if c.Gmail.MaxResults <= 0 {
		problems = append(problems, "gmail.max_results must be greater than zero")
	}
	if c.Gmail.ClientID == "" {
		problems = append(problems, "gmail.client_id is required")
	}
	if c.Gmail.ClientSecret == "" {
		problems = append(problems, "gmail.client_secret is required")
	}
	if c.Gmail.Watch.Enabled {
		if c.Gmail.Watch.ProjectID == "" {
			problems = append(problems, "gmail.watch.project_id is required when watch is enabled")
		}
		if c.Gmail.Watch.TopicName == "" {
			problems = append(problems, "gmail.watch.topic_name is required when watch is enabled")
		}
		if c.Gmail.Watch.SubscriptionID == "" {
			problems = append(problems, "gmail.watch.subscription_id is required when watch is enabled")
		}
		if c.Gmail.Watch.RenewBefore <= 0 {
			problems = append(problems, "gmail.watch.renew_before must be greater than zero when watch is enabled")
		}
	}

	if len(problems) == 0 {
		return nil
	}

	return errors.New(strings.Join(problems, "; "))
}

func GmailServiceConfig(cfg Config) gmailconfig.ServiceConfig {
	workers := cfg.Gmail.Workers
	if workers <= 0 {
		workers = 1
	}

	queueSize := workers * int(cfg.Gmail.MaxResults)
	if queueSize < workers*2 {
		queueSize = workers * 2
	}
	if queueSize <= 0 {
		queueSize = workers
	}

	return gmailconfig.ServiceConfig{
		FetchInterval:  cfg.Gmail.FetchInterval,
		CommandTimeout: cfg.Gmail.CommandTimeout,
		Workers:        workers,
		QueueSize:      queueSize,
	}
}

func decodeYAML(r io.Reader, out *Config) error {
	decoder := yaml.NewDecoder(r)
	decoder.KnownFields(true)
	err := decoder.Decode(out)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}
