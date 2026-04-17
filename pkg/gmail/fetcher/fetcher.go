package fetcher

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	gmailapi "google.golang.org/api/gmail/v1"
	gmailconfig "moon-shell/pkg/gmail/config"
)

type Result struct {
	Mailbox string
	Subject string
	Items   []Item
	Found   bool
}

type Item struct {
	ID              string
	Kind            string
	Body            string
	Labels          []string
	From            string
	ThreadID        string
	RFC822MessageID string
	Subject         string
	InternalDate    time.Time
}

type Fetcher struct {
	cfg gmailconfig.Config
	svc *gmailapi.Service
}

func New(cfg gmailconfig.Config, svc *gmailapi.Service) *Fetcher {
	return &Fetcher{cfg: cfg, svc: svc}
}

func (f *Fetcher) Fetch(ctx context.Context) (Result, error) {
	profile, err := f.svc.Users.GetProfile(f.cfg.User).Context(ctx).Do()
	if err != nil {
		return Result{}, fmt.Errorf("get mailbox profile: %w", err)
	}

	drafts, err := f.findDrafts(ctx, f.cfg.Subject)
	if err != nil {
		return Result{}, err
	}

	result := Result{
		Mailbox: profile.EmailAddress,
		Subject: f.cfg.Subject,
	}
	for _, draft := range drafts {
		body := extractBody(draft.Message.Payload)
		if body == "" {
			body = draft.Message.Snippet
		}

		result.Items = append(result.Items, Item{
			ID:       draft.Id,
			Kind:     "draft",
			Body:     body,
			ThreadID: draft.Message.ThreadId,
			Subject:  subjectFromPayload(draft.Message.Payload),
		})
	}

	messages, err := f.findMailboxMessages(ctx, f.cfg.Subject)
	if err != nil {
		return Result{}, err
	}
	for _, message := range messages {
		body := extractBody(message.Payload)
		if body == "" {
			body = message.Snippet
		}

		result.Items = append(result.Items, Item{
			ID:              message.Id,
			Kind:            messageKind(message.LabelIds),
			Body:            body,
			Labels:          append([]string(nil), message.LabelIds...),
			From:            headerValue(message.Payload, "From"),
			ThreadID:        message.ThreadId,
			RFC822MessageID: headerValue(message.Payload, "Message-ID"),
			Subject:         subjectFromPayload(message.Payload),
			InternalDate:    parseInternalDate(message.InternalDate),
		})
	}

	result.Found = len(result.Items) > 0

	return result, nil
}

func (f *Fetcher) findDrafts(ctx context.Context, subject string) ([]*gmailapi.Draft, error) {
	req := f.svc.Users.Drafts.List(f.cfg.User).Context(ctx).MaxResults(f.cfg.MaxResults)
	var drafts []*gmailapi.Draft

	for {
		resp, err := req.Do()
		if err != nil {
			return nil, fmt.Errorf("list drafts: %w", err)
		}

		for _, item := range resp.Drafts {
			draft, err := f.svc.Users.Drafts.Get(f.cfg.User, item.Id).Context(ctx).Format("full").Do()
			if err != nil {
				return nil, fmt.Errorf("get draft %s: %w", item.Id, err)
			}

			if subjectMatches(subjectFromPayload(draft.Message.Payload), subject) {
				drafts = append(drafts, draft)
			}
		}

		if resp.NextPageToken == "" {
			return drafts, nil
		}

		req.PageToken(resp.NextPageToken)
	}
}

func (f *Fetcher) findMailboxMessages(ctx context.Context, subject string) ([]*gmailapi.Message, error) {
	query := fmt.Sprintf("in:anywhere subject:%q", subject)
	req := f.svc.Users.Messages.List(f.cfg.User).
		Context(ctx).
		IncludeSpamTrash(true).
		Q(query).
		MaxResults(f.cfg.MaxResults)
	var messages []*gmailapi.Message

	for {
		resp, err := req.Do()
		if err != nil {
			return nil, fmt.Errorf("list inbox/spam messages: %w", err)
		}

		for _, item := range resp.Messages {
			message, err := f.svc.Users.Messages.Get(f.cfg.User, item.Id).Context(ctx).Format("full").Do()
			if err != nil {
				return nil, fmt.Errorf("get message %s: %w", item.Id, err)
			}

			if !hasLabel(message.LabelIds, "INBOX") && !hasLabel(message.LabelIds, "SPAM") {
				continue
			}
			if subjectMatches(subjectFromPayload(message.Payload), subject) {
				messages = append(messages, message)
			}
		}

		if resp.NextPageToken == "" {
			return messages, nil
		}

		req.PageToken(resp.NextPageToken)
	}
}

func subjectFromPayload(payload *gmailapi.MessagePart) string {
	return headerValue(payload, "Subject")
}

func subjectMatches(value, want string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	want = strings.ToLower(strings.TrimSpace(want))
	if value == "" || want == "" {
		return false
	}
	return strings.Contains(value, want)
}

func headerValue(payload *gmailapi.MessagePart, name string) string {
	if payload == nil {
		return ""
	}

	for _, header := range payload.Headers {
		if strings.EqualFold(header.Name, name) {
			return header.Value
		}
	}

	return ""
}

func extractBody(payload *gmailapi.MessagePart) string {
	if payload == nil {
		return ""
	}

	if strings.HasPrefix(payload.MimeType, "text/plain") {
		if body := decodeBody(payload.Body); body != "" {
			return body
		}
	}

	for _, part := range payload.Parts {
		if body := extractBody(part); body != "" {
			return body
		}
	}

	if body := decodeBody(payload.Body); body != "" {
		return body
	}

	return ""
}

func decodeBody(body *gmailapi.MessagePartBody) string {
	if body == nil || body.Data == "" {
		return ""
	}

	decoded, err := base64.URLEncoding.DecodeString(body.Data)
	if err != nil {
		decoded, err = base64.RawURLEncoding.DecodeString(body.Data)
		if err != nil {
			return ""
		}
	}

	return strings.TrimSpace(string(decoded))
}

func hasLabel(labels []string, target string) bool {
	for _, label := range labels {
		if label == target {
			return true
		}
	}

	return false
}

func messageKind(labels []string) string {
	switch {
	case hasLabel(labels, "INBOX") && hasLabel(labels, "SPAM"):
		return "message[inbox,spam]"
	case hasLabel(labels, "INBOX"):
		return "message[inbox]"
	case hasLabel(labels, "SPAM"):
		return "message[spam]"
	default:
		return "message"
	}
}

func parseInternalDate(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}

	return time.UnixMilli(ms).UTC()
}
