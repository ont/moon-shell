package responder

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"strings"
	"time"

	gmailapi "google.golang.org/api/gmail/v1"
	"moon-shell/pkg/commandexec"
	gmailconfig "moon-shell/pkg/gmail/config"
	"moon-shell/pkg/gmail/fetcher"
)

type Responder struct {
	user string
	svc  *gmailapi.Service
}

func NewResponder(cfg gmailconfig.Config, svc *gmailapi.Service) *Responder {
	return &Responder{user: cfg.User, svc: svc}
}

func (r *Responder) Send(ctx context.Context, item fetcher.Item, result commandexec.Result) error {
	to, err := parseFirstAddress(item.From)
	if err != nil {
		return fmt.Errorf("parse recipient address %q: %w", item.From, err)
	}

	raw, err := buildReply(to, item, result)
	if err != nil {
		return err
	}

	message := &gmailapi.Message{
		Raw:      base64.RawURLEncoding.EncodeToString(raw),
		ThreadId: item.ThreadID,
	}

	_, err = r.svc.Users.Messages.Send(r.user, message).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("send gmail response: %w", err)
	}

	return nil
}

func buildReply(to string, item fetcher.Item, result commandexec.Result) ([]byte, error) {
	body := formatBody(item, result)

	subject := item.Subject
	if !strings.HasPrefix(strings.ToLower(subject), "re:") {
		subject = "Re: " + subject
	}

	multipartBody, boundary, err := buildMultipartBody(body, result)
	if err != nil {
		return nil, err
	}

	var msg bytes.Buffer
	fmt.Fprintf(&msg, "To: %s\r\n", to)
	fmt.Fprintf(&msg, "Subject: %s\r\n", subject)
	if item.RFC822MessageID != "" {
		fmt.Fprintf(&msg, "In-Reply-To: %s\r\n", item.RFC822MessageID)
		fmt.Fprintf(&msg, "References: %s\r\n", item.RFC822MessageID)
	}
	msg.WriteString("MIME-Version: 1.0\r\n")
	fmt.Fprintf(&msg, "Content-Type: multipart/mixed; boundary=%q\r\n", boundary)
	msg.WriteString("\r\n")
	msg.Write(multipartBody)

	return msg.Bytes(), nil
}

func buildMultipartBody(body string, result commandexec.Result) ([]byte, string, error) {
	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)

	if err := writeTextPart(writer, body); err != nil {
		return nil, "", err
	}
	if err := writeAttachmentPart(writer, "stdout.txt", result.Stdout); err != nil {
		return nil, "", err
	}
	if err := writeAttachmentPart(writer, "stderr.txt", result.Stderr); err != nil {
		return nil, "", err
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("finalize multipart body: %w", err)
	}

	return payload.Bytes(), writer.Boundary(), nil
}

func writeTextPart(writer *multipart.Writer, body string) error {
	header := textproto.MIMEHeader{}
	header.Set("Content-Type", "text/plain; charset=UTF-8")
	header.Set("Content-Transfer-Encoding", "quoted-printable")

	part, err := writer.CreatePart(header)
	if err != nil {
		return fmt.Errorf("create response body part: %w", err)
	}

	qp := quotedprintable.NewWriter(part)
	if _, err := qp.Write([]byte(body)); err != nil {
		return fmt.Errorf("encode response body: %w", err)
	}
	if err := qp.Close(); err != nil {
		return fmt.Errorf("finalize response body encoding: %w", err)
	}

	return nil
}

func writeAttachmentPart(writer *multipart.Writer, filename, content string) error {
	header := textproto.MIMEHeader{}
	header.Set("Content-Type", fmt.Sprintf("text/plain; charset=UTF-8; name=%q", filename))
	header.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	header.Set("Content-Transfer-Encoding", "base64")

	part, err := writer.CreatePart(header)
	if err != nil {
		return fmt.Errorf("create attachment %s: %w", filename, err)
	}

	if _, err := part.Write([]byte(wrapBase64(base64.StdEncoding.EncodeToString([]byte(content))))); err != nil {
		return fmt.Errorf("write attachment %s: %w", filename, err)
	}

	return nil
}

func wrapBase64(value string) string {
	if value == "" {
		return ""
	}

	var wrapped strings.Builder
	for len(value) > 76 {
		wrapped.WriteString(value[:76])
		wrapped.WriteString("\r\n")
		value = value[76:]
	}
	wrapped.WriteString(value)
	return wrapped.String()
}

func formatBody(item fetcher.Item, result commandexec.Result) string {
	return fmt.Sprintf(
		"moon-shell execution response\n\n"+
			"message_id: %s\n"+
			"kind: %s\n"+
			"executed_at: %s\n"+
			"command:\n%s\n\n"+
			"exit_code: %d\n\n"+
			"stdout:\n%s\n\n"+
			"stderr:\n%s\n",
		item.ID,
		item.Kind,
		time.Now().UTC().Format(time.RFC3339),
		result.Command,
		result.ExitCode,
		emptyIfBlank(result.Stdout),
		emptyIfBlank(result.Stderr),
	)
}

func parseFirstAddress(value string) (string, error) {
	addresses, err := mail.ParseAddressList(value)
	if err != nil {
		return "", err
	}
	if len(addresses) == 0 {
		return "", fmt.Errorf("no address found")
	}
	return addresses[0].Address, nil
}

func emptyIfBlank(value string) string {
	if strings.TrimSpace(value) == "" {
		return "<empty>"
	}
	return value
}
