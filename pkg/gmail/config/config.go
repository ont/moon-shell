package config

import "time"

type Config struct {
	User           string        `yaml:"user"`
	Subject        string        `yaml:"subject"`
	FetchInterval  time.Duration `yaml:"fetch_interval"`
	CommandTimeout time.Duration `yaml:"command_timeout"`
	Workers        int           `yaml:"workers"`
	MaxResults     int64         `yaml:"max_results"`
	ClientID       string        `yaml:"client_id"`
	ClientSecret   string        `yaml:"client_secret"`
	Watch          WatchConfig   `yaml:"watch"`
}

type WatchConfig struct {
	Enabled         bool          `yaml:"enabled"`
	ProjectID       string        `yaml:"project_id"`
	TopicName       string        `yaml:"topic_name"`
	SubscriptionID  string        `yaml:"subscription_id"`
	CredentialsFile string        `yaml:"credentials_file"`
	RenewBefore     time.Duration `yaml:"renew_before"`
}

type ServiceConfig struct {
	FetchInterval  time.Duration
	CommandTimeout time.Duration
	Workers        int
	QueueSize      int
}

func (c ServiceConfig) GetCommandTimeout() time.Duration {
	return c.CommandTimeout
}
