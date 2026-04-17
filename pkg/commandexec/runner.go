package commandexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

const defaultTimeout = time.Minute

type Result struct {
	Command  string
	Stdout   string
	Stderr   string
	ExitCode int
}

type Runner struct {
	timeout time.Duration
}

type TimeoutConfig interface {
	GetCommandTimeout() time.Duration
}

func NewRunner(cfg TimeoutConfig) *Runner {
	timeout := cfg.GetCommandTimeout()
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Runner{timeout: timeout}
}

func (r *Runner) Execute(ctx context.Context, command string) (Result, error) {
	return r.ExecuteIn(ctx, command, "")
}

func (r *Runner) ExecuteIn(ctx context.Context, command, dir string) (Result, error) {
	if r.timeout <= 0 {
		r.timeout = defaultTimeout
	}

	runCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "bash", "-lc", command)
	if dir != "" {
		cmd.Dir = dir
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := Result{
		Command: command,
		Stdout:  stdout.String(),
		Stderr:  stderr.String(),
	}

	var exitErr *exec.ExitError
	switch {
	case err == nil:
		result.ExitCode = 0
		return result, nil
	case errors.As(err, &exitErr):
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	case runCtx.Err() != nil:
		result.ExitCode = -1
		if result.Stderr == "" {
			result.Stderr = runCtx.Err().Error()
		}
		return result, nil
	default:
		return result, fmt.Errorf("execute command: %w", err)
	}
}
