package gogmoon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
)

type Gog struct {
	binary  string
	account string
}

func NewGog(cfg GogConfig) *Gog {
	binary := cfg.Binary
	if binary == "" {
		binary = "gog"
	}
	return &Gog{binary: binary, account: cfg.Account}
}

func (g *Gog) Search(ctx context.Context, query string, max int) ([]json.RawMessage, error) {
	candidates := [][]string{
		{"gmail", "messages", "search", query, "--account", g.account, "--max", strconv.Itoa(max), "--json"},
		{"gmail", "search", query, "--account", g.account, "--max", strconv.Itoa(max), "--json"},
	}
	var lastErr error
	for _, args := range candidates {
		out, err := g.run(ctx, args...)
		if err != nil {
			lastErr = err
			continue
		}
		return decodeRawMessages(out)
	}
	return nil, lastErr
}

func (g *Gog) GetMessage(ctx context.Context, messageID string) (json.RawMessage, error) {
	candidates := [][]string{
		{"gmail", "get", messageID, "--account", g.account, "--format", "full", "--json"},
		{"gmail", "message", "get", messageID, "--account", g.account, "--format", "full", "--json"},
		{"gmail", "messages", "get", messageID, "--account", g.account, "--format", "full", "--json"},
	}
	return g.runFirstJSON(ctx, candidates)
}

func (g *Gog) SaveAttachment(ctx context.Context, messageID, attachmentID, filename string) error {
	candidates := [][]string{
		{"gmail", "attachment", messageID, attachmentID, "--account", g.account, "--out", filename},
		{"gmail", "attachments", "get", messageID, attachmentID, "--account", g.account, "--out", filename},
		{"gmail", "message", "attachment", messageID, attachmentID, "--account", g.account, "--out", filename},
	}
	var lastErr error
	for _, args := range candidates {
		if _, err := g.run(ctx, args...); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	return fmt.Errorf("download attachment %s for message %s: %w", attachmentID, messageID, lastErr)
}

func (g *Gog) SendReply(ctx context.Context, reply ReplyRequest) error {
	args := []string{
		"gmail", "send",
		"--account", g.account,
		"--to", reply.To,
		"--subject", reply.Subject,
		"--body-file", reply.BodyFile,
	}
	if reply.ReplyToMessageID != "" {
		args = append(args, "--reply-to-message-id", reply.ReplyToMessageID)
	}
	for _, attachment := range reply.Attachments {
		args = append(args, "--attach", attachment)
	}

	if _, err := g.run(ctx, args...); err != nil {
		return fmt.Errorf("send gmail response: %w", err)
	}
	return nil
}

func (g *Gog) MarkRead(ctx context.Context, messageID string) error {
	args := []string{
		"gmail", "batch", "modify", messageID,
		"--account", g.account,
		"--remove", "UNREAD",
		"--force",
		"--no-input",
	}
	if _, err := g.run(ctx, args...); err != nil {
		return fmt.Errorf("mark message %s as read: %w", messageID, err)
	}
	return nil
}

func (g *Gog) runFirstJSON(ctx context.Context, candidates [][]string) (json.RawMessage, error) {
	var lastErr error
	for _, args := range candidates {
		out, err := g.run(ctx, args...)
		if err != nil {
			lastErr = err
			continue
		}
		if !json.Valid(out) {
			lastErr = fmt.Errorf("command returned non-json output")
			continue
		}
		return append(json.RawMessage(nil), out...), nil
	}
	return nil, lastErr
}

func (g *Gog) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, g.binary, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return nil, fmt.Errorf("%s %v: %w: %s", g.binary, args, err, stderr.String())
		}
		return nil, fmt.Errorf("%s %v: %w", g.binary, args, err)
	}
	return stdout.Bytes(), nil
}

func decodeRawMessages(data []byte) ([]json.RawMessage, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(data, &items); err == nil {
		return items, nil
	}

	var envelope struct {
		Messages []json.RawMessage `json:"messages"`
		Items    []json.RawMessage `json:"items"`
		Results  []json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("decode gog search json: %w", err)
	}
	switch {
	case len(envelope.Messages) > 0:
		return envelope.Messages, nil
	case len(envelope.Items) > 0:
		return envelope.Items, nil
	default:
		return envelope.Results, nil
	}
}

type ReplyRequest struct {
	To               string
	Subject          string
	ReplyToMessageID string
	BodyFile         string
	Attachments      []string
}
