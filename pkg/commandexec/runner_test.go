package commandexec

import (
	"context"
	"strings"
	"testing"
	"time"
)

type testTimeoutConfig struct {
	timeout time.Duration
}

func (c testTimeoutConfig) GetCommandTimeout() time.Duration {
	return c.timeout
}

func TestExecuteInReturnsAfterTimeoutWithChildProcess(t *testing.T) {
	runner := NewRunner(testTimeoutConfig{timeout: 100 * time.Millisecond})

	start := time.Now()
	result, err := runner.ExecuteIn(context.Background(), "sleep 5", "")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("ExecuteIn() error = %v", err)
	}
	if elapsed > time.Second {
		t.Fatalf("ExecuteIn() elapsed = %s, want under 1s", elapsed)
	}
	if result.ExitCode != -1 {
		t.Fatalf("ExitCode = %d, want -1", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, context.DeadlineExceeded.Error()) {
		t.Fatalf("Stderr = %q, want deadline exceeded", result.Stderr)
	}
}

func TestExecuteInReturnsAfterTimeoutWithNestedChildProcess(t *testing.T) {
	runner := NewRunner(testTimeoutConfig{timeout: 100 * time.Millisecond})

	start := time.Now()
	result, err := runner.ExecuteIn(context.Background(), "sh -c 'sleep 5'", "")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("ExecuteIn() error = %v", err)
	}
	if elapsed > time.Second {
		t.Fatalf("ExecuteIn() elapsed = %s, want under 1s", elapsed)
	}
	if result.ExitCode != -1 {
		t.Fatalf("ExitCode = %d, want -1", result.ExitCode)
	}
	if !strings.Contains(result.Stderr, context.DeadlineExceeded.Error()) {
		t.Fatalf("Stderr = %q, want deadline exceeded", result.Stderr)
	}
}
