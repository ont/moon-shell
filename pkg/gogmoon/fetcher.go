package gogmoon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type FetchResult struct {
	Mailbox string
	Subject string
	Items   []Message
	Found   bool
}

type Fetcher struct {
	cfg GogConfig
	gog *Gog
}

func NewFetcher(cfg GogConfig, gog *Gog) *Fetcher {
	return &Fetcher{cfg: cfg, gog: gog}
}

func (f *Fetcher) Fetch(ctx context.Context) (FetchResult, error) {
	seen := make(map[string]struct{})
	var messages []Message

	for _, query := range f.searchQueries() {
		rawItems, err := f.gog.Search(ctx, query, f.cfg.MaxResults)
		if err != nil {
			return FetchResult{}, fmt.Errorf("search %q: %w", query, err)
		}

		for _, raw := range rawItems {
			id := messageID(raw)
			if id == "" {
				continue
			}
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}

			fullRaw, err := f.gog.GetMessage(ctx, id)
			if err != nil {
				return FetchResult{}, fmt.Errorf("get message %s: %w", id, err)
			}
			message, err := MessageFromJSON(fullRaw)
			if err != nil {
				return FetchResult{}, fmt.Errorf("parse message %s: %w", id, err)
			}
			if message.ID == "" {
				message.ID = id
			}
			if !SubjectMatches(message.Subject, f.cfg.Subject) {
				continue
			}
			messages = append(messages, message)
		}
	}

	SortMessagesOlderFirst(messages)
	return FetchResult{
		Mailbox: f.cfg.Account,
		Subject: f.cfg.Subject,
		Items:   messages,
		Found:   len(messages) > 0,
	}, nil
}

func (f *Fetcher) searchQueries() []string {
	queries := make([]string, 0, len(f.cfg.SearchQueries))
	for _, base := range f.cfg.SearchQueries {
		query := strings.TrimSpace(base)
		if query == "" {
			continue
		}
		if f.cfg.UnreadOnly && !containsQueryTerm(query, "is:unread") {
			query += " is:unread"
		}
		if f.cfg.SearchSubject && !containsQueryTerm(query, "subject:") {
			query += " subject:" + quoteGmailSearchValue(f.cfg.Subject)
		}
		queries = append(queries, query)
	}
	return queries
}

func containsQueryTerm(query, term string) bool {
	return strings.Contains(strings.ToLower(query), strings.ToLower(term))
}

func quoteGmailSearchValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}

func messageID(data json.RawMessage) string {
	for _, field := range []string{"id", "messageId", "message_id"} {
		if value := stringField(data, field); value != "" {
			return value
		}
	}
	return ""
}
