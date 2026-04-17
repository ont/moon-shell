package gogmoon

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/mail"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Message struct {
	ID              string
	ThreadID        string
	Subject         string
	From            string
	RFC822MessageID string
	InternalDate    time.Time
	Labels          []string
	Body            string
	Raw             json.RawMessage
	Attachments     []Attachment
}

type Attachment struct {
	AttachmentID string
	Filename     string
	MimeType     string
	Data         []byte
}

type gmailMessage struct {
	ID           string          `json:"id"`
	ThreadID     string          `json:"threadId"`
	LabelIDs     []string        `json:"labelIds"`
	Snippet      string          `json:"snippet"`
	InternalDate json.RawMessage `json:"internalDate"`
	Payload      *messagePart    `json:"payload"`
}

type messagePart struct {
	PartID   string        `json:"partId"`
	MimeType string        `json:"mimeType"`
	Filename string        `json:"filename"`
	Headers  []header      `json:"headers"`
	Body     partBody      `json:"body"`
	Parts    []messagePart `json:"parts"`
}

type partBody struct {
	AttachmentID string `json:"attachmentId"`
	Data         string `json:"data"`
	Size         int64  `json:"size"`
}

type header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func MessageFromJSON(data json.RawMessage) (Message, error) {
	data, gogBody, gogHeaders := unwrapGogMessage(data)

	var gm gmailMessage
	if err := json.Unmarshal(data, &gm); err != nil {
		return Message{}, fmt.Errorf("decode message json: %w", err)
	}

	msg := Message{
		ID:           firstNonEmpty(gm.ID, stringField(data, "messageId")),
		ThreadID:     gm.ThreadID,
		Labels:       append([]string(nil), gm.LabelIDs...),
		Body:         gm.Snippet,
		Raw:          append(json.RawMessage(nil), data...),
		InternalDate: parseDate(data, gm.InternalDate),
	}
	if gm.Payload != nil {
		msg.Subject = headerValue(gm.Payload.Headers, "Subject")
		msg.From = headerValue(gm.Payload.Headers, "From")
		msg.RFC822MessageID = headerValue(gm.Payload.Headers, "Message-ID")
		msg.Body = firstNonEmpty(extractBody(gm.Payload), gm.Snippet)
		msg.Attachments = collectAttachments(gm.Payload)
	}

	msg.Subject = firstNonEmpty(msg.Subject, gogHeaders["subject"], stringField(data, "subject"))
	msg.From = firstNonEmpty(msg.From, gogHeaders["from"], stringField(data, "from"))
	msg.RFC822MessageID = firstNonEmpty(msg.RFC822MessageID, gogHeaders["message-id"])
	msg.Body = firstNonEmpty(gogBody, msg.Body)
	return msg, nil
}

func unwrapGogMessage(data json.RawMessage) (json.RawMessage, string, map[string]string) {
	var envelope struct {
		Body    string          `json:"body"`
		Headers map[string]any  `json:"headers"`
		Message json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil || len(envelope.Message) == 0 {
		return data, "", nil
	}

	headers := make(map[string]string, len(envelope.Headers))
	for name, value := range envelope.Headers {
		if text, ok := value.(string); ok {
			headers[strings.ToLower(name)] = text
		}
	}
	return envelope.Message, envelope.Body, headers
}

func SortMessagesOlderFirst(messages []Message) {
	sort.SliceStable(messages, func(i, j int) bool {
		left := messages[i].InternalDate
		right := messages[j].InternalDate
		switch {
		case left.IsZero() && right.IsZero():
			return messages[i].ID < messages[j].ID
		case left.IsZero():
			return false
		case right.IsZero():
			return true
		default:
			return left.Before(right)
		}
	})
}

func SubjectMatches(value, want string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	want = strings.ToLower(strings.TrimSpace(want))
	return value != "" && want != "" && strings.Contains(value, want)
}

func RecipientAddress(value string) (string, error) {
	addresses, err := mail.ParseAddressList(value)
	if err != nil {
		return "", err
	}
	if len(addresses) == 0 {
		return "", fmt.Errorf("no address found")
	}
	return addresses[0].Address, nil
}

func ResponseSubject(subject string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(subject)), "re:") {
		return subject
	}
	return "Re: " + subject
}

func headerValue(headers []header, name string) string {
	for _, header := range headers {
		if strings.EqualFold(header.Name, name) {
			return header.Value
		}
	}
	return ""
}

func extractBody(part *messagePart) string {
	if part == nil {
		return ""
	}
	if strings.HasPrefix(part.MimeType, "text/plain") {
		if body := decodeBodyData(part.Body.Data); body != "" {
			return body
		}
	}
	for i := range part.Parts {
		if body := extractBody(&part.Parts[i]); body != "" {
			return body
		}
	}
	if body := decodeBodyData(part.Body.Data); body != "" && strings.TrimSpace(part.Filename) == "" {
		return body
	}
	return ""
}

func collectAttachments(part *messagePart) []Attachment {
	if part == nil {
		return nil
	}

	var attachments []Attachment
	walkParts(part, func(current *messagePart) {
		if current.Filename == "" {
			return
		}
		attachment := Attachment{
			AttachmentID: current.Body.AttachmentID,
			Filename:     cleanFilename(current.Filename),
			MimeType:     current.MimeType,
			Data:         decodeBodyBytes(current.Body.Data),
		}
		attachments = append(attachments, attachment)
	})
	return attachments
}

func walkParts(part *messagePart, fn func(*messagePart)) {
	fn(part)
	for i := range part.Parts {
		walkParts(&part.Parts[i], fn)
	}
}

func decodeBodyData(value string) string {
	data := decodeBodyBytes(value)
	return strings.TrimSpace(string(data))
}

func decodeBodyBytes(value string) []byte {
	if value == "" {
		return nil
	}
	decoded, err := base64.URLEncoding.DecodeString(value)
	if err != nil {
		decoded, err = base64.RawURLEncoding.DecodeString(value)
		if err != nil {
			return nil
		}
	}
	return decoded
}

func parseDate(data json.RawMessage, internalDate json.RawMessage) time.Time {
	if parsed := parseInternalDate(internalDate); !parsed.IsZero() {
		return parsed
	}
	for _, key := range []string{"date", "internalDate"} {
		if parsed := parseTimeString(stringField(data, key)); !parsed.IsZero() {
			return parsed
		}
	}
	return time.Time{}
}

func parseInternalDate(value json.RawMessage) time.Time {
	if len(value) == 0 {
		return time.Time{}
	}
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		text = string(value)
	}
	text = strings.Trim(text, `"`)
	ms, err := strconv.ParseInt(text, 10, 64)
	if err != nil || ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

func parseTimeString(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed.UTC()
	}
	if parsed, err := mail.ParseDate(value); err == nil {
		return parsed.UTC()
	}
	return time.Time{}
}

func stringField(data json.RawMessage, name string) string {
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		return ""
	}
	value, ok := fields[name]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	default:
		return ""
	}
}

func cleanFilename(name string) string {
	decoder := mime.WordDecoder{}
	if decoded, err := decoder.DecodeHeader(name); err == nil && decoded != "" {
		name = decoded
	}
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.TrimSpace(name)
	if name == "." || name == "/" || name == "" {
		return "attachment"
	}
	return name
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
