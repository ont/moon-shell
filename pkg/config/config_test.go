package config

import "testing"

func TestDefaultSetsCommandExecutionDefaults(t *testing.T) {
	cfg := Default()

	if cfg.Gmail.CommandTimeout <= 0 {
		t.Fatalf("CommandTimeout = %v, want > 0", cfg.Gmail.CommandTimeout)
	}
	if cfg.Gmail.Workers <= 0 {
		t.Fatalf("Workers = %d, want > 0", cfg.Gmail.Workers)
	}
}

func TestGmailServiceConfigSetsQueueSize(t *testing.T) {
	cfg := Default()
	cfg.Gmail.Workers = 3
	cfg.Gmail.MaxResults = 5

	serviceCfg := GmailServiceConfig(cfg)
	if serviceCfg.Workers != 3 {
		t.Fatalf("Workers = %d, want 3", serviceCfg.Workers)
	}
	if serviceCfg.QueueSize != 15 {
		t.Fatalf("QueueSize = %d, want 15", serviceCfg.QueueSize)
	}
}
