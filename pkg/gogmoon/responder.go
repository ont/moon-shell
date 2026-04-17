package gogmoon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"moon-shell/pkg/commandexec"
)

type Responder struct {
	gog *Gog
}

func NewResponder(gog *Gog) *Responder {
	return &Responder{gog: gog}
}

func (r *Responder) Send(ctx context.Context, workspace Workspace, message Message, result commandexec.Result) error {
	to, err := RecipientAddress(message.From)
	if err != nil {
		return fmt.Errorf("parse recipient address %q: %w", message.From, err)
	}

	bodyPath := filepath.Join(workspace.Dir, "response-body.txt")
	if err := os.WriteFile(bodyPath, []byte(formatResponseBody(message, result, workspace)), 0o600); err != nil {
		return fmt.Errorf("write response body: %w", err)
	}
	stdoutPath := filepath.Join(workspace.Dir, "stdout.txt")
	if err := os.WriteFile(stdoutPath, []byte(result.Stdout), 0o600); err != nil {
		return fmt.Errorf("write stdout attachment: %w", err)
	}
	stderrPath := filepath.Join(workspace.Dir, "stderr.txt")
	if err := os.WriteFile(stderrPath, []byte(result.Stderr), 0o600); err != nil {
		return fmt.Errorf("write stderr attachment: %w", err)
	}

	return r.gog.SendReply(ctx, ReplyRequest{
		To:               to,
		Subject:          ResponseSubject(message.Subject),
		ReplyToMessageID: message.ID,
		BodyFile:         bodyPath,
		Attachments:      []string{stdoutPath, stderrPath},
	})
}

func formatResponseBody(message Message, result commandexec.Result, workspace Workspace) string {
	return fmt.Sprintf(
		"moon-shell gog execution response\n\n"+
			"message_id: %s\n"+
			"executed_at: %s\n"+
			"workspace: %s\n"+
			"command:\n%s\n\n"+
			"exit_code: %d\n\n"+
			"stdout:\n%s\n\n"+
			"stderr:\n%s\n",
		message.ID,
		time.Now().UTC().Format(time.RFC3339),
		workspace.Dir,
		result.Command,
		result.ExitCode,
		emptyIfBlank(result.Stdout),
		emptyIfBlank(result.Stderr),
	)
}

func emptyIfBlank(value string) string {
	if strings.TrimSpace(value) == "" {
		return "<empty>"
	}
	return value
}
