package commandexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const defaultTimeout = time.Minute
const killWaitTimeout = 2 * time.Second

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

	cmd := exec.Command("bash", "-lc", command)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdoutPath, stderrPath, cleanup, err := prepareOutputFiles(cmd)
	if err != nil {
		return Result{Command: command}, err
	}
	defer cleanup()

	if err := cmd.Start(); err != nil {
		return Result{Command: command}, fmt.Errorf("start command: %w", err)
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	var waitErr error
	timedOut := false
	select {
	case waitErr = <-waitCh:
	case <-runCtx.Done():
		timedOut = true
		killProcessGroup(cmd)
		select {
		case waitErr = <-waitCh:
		case <-time.After(killWaitTimeout):
			waitErr = runCtx.Err()
		}
	}

	stdout, stderr, readErr := readOutputFiles(stdoutPath, stderrPath)
	if readErr != nil {
		return Result{Command: command}, readErr
	}

	result := Result{
		Command: command,
		Stdout:  stdout,
		Stderr:  stderr,
	}

	var exitErr *exec.ExitError
	switch {
	case waitErr == nil:
		result.ExitCode = 0
		return result, nil
	case timedOut || runCtx.Err() != nil:
		result.ExitCode = -1
		if result.Stderr == "" {
			result.Stderr = runCtx.Err().Error()
		}
		return result, nil
	case errors.As(waitErr, &exitErr):
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	default:
		return result, fmt.Errorf("execute command: %w", waitErr)
	}
}

func prepareOutputFiles(cmd *exec.Cmd) (string, string, func(), error) {
	stdout, err := os.CreateTemp("", "moon-shell-stdout-*")
	if err != nil {
		return "", "", nil, fmt.Errorf("create stdout file: %w", err)
	}
	stderr, err := os.CreateTemp("", "moon-shell-stderr-*")
	if err != nil {
		_ = stdout.Close()
		_ = os.Remove(stdout.Name())
		return "", "", nil, fmt.Errorf("create stderr file: %w", err)
	}

	cmd.Stdout = stdout
	cmd.Stderr = stderr

	cleanup := func() {
		_ = stdout.Close()
		_ = stderr.Close()
		_ = os.Remove(stdout.Name())
		_ = os.Remove(stderr.Name())
	}
	return stdout.Name(), stderr.Name(), cleanup, nil
}

func readOutputFiles(stdoutPath, stderrPath string) (string, string, error) {
	stdout, err := os.ReadFile(stdoutPath)
	if err != nil {
		return "", "", fmt.Errorf("read stdout file: %w", err)
	}
	stderr, err := os.ReadFile(stderrPath)
	if err != nil {
		return "", "", fmt.Errorf("read stderr file: %w", err)
	}
	return string(stdout), string(stderr), nil
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		_ = cmd.Process.Kill()
	}
}
