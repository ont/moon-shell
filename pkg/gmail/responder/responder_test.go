package responder

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"

	"moon-shell/pkg/commandexec"
	"moon-shell/pkg/gmail/fetcher"
)

func TestBuildReplyAddsStdoutAndStderrAttachments(t *testing.T) {
	item := fetcher.Item{
		From:            "sender@example.com",
		Subject:         "moon-shell",
		ThreadID:        "thread-1",
		RFC822MessageID: "<message-id>",
		ID:              "msg-1",
		Kind:            "message[inbox]",
	}
	result := commandexec.Result{
		Command:  "pwd",
		Stdout:   "/tmp\n",
		Stderr:   "warning\n",
		ExitCode: 0,
	}

	raw, err := buildReply("sender@example.com", item, result)
	if err != nil {
		t.Fatalf("buildReply() error = %v", err)
	}

	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ReadMessage() error = %v", err)
	}

	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("ParseMediaType() error = %v", err)
	}
	if mediaType != "multipart/mixed" {
		t.Fatalf("mediaType = %q, want multipart/mixed", mediaType)
	}

	reader := multipart.NewReader(msg.Body, params["boundary"])
	parts := map[string]string{}
	var textBody string

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextPart() error = %v", err)
		}

		data, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("ReadAll(part) error = %v", err)
		}

		filename := part.FileName()
		if filename == "" {
			textBody = string(data)
			continue
		}

		decoded, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, bytes.NewReader(data)))
		if err != nil {
			t.Fatalf("base64 decode %s error = %v", filename, err)
		}
		parts[filename] = string(decoded)
	}

	if !strings.Contains(textBody, "moon-shell execution response") {
		t.Fatalf("text body = %q, want summary text", textBody)
	}
	if parts["stdout.txt"] != result.Stdout {
		t.Fatalf("stdout attachment = %q, want %q", parts["stdout.txt"], result.Stdout)
	}
	if parts["stderr.txt"] != result.Stderr {
		t.Fatalf("stderr attachment = %q, want %q", parts["stderr.txt"], result.Stderr)
	}
}
