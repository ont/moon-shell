package watcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"cloud.google.com/go/pubsub"
	gmailapi "google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"
	gmailconfig "moon-shell/pkg/gmail/config"
)

type Event struct {
	EmailAddress string    `json:"email_address"`
	HistoryID    uint64    `json:"history_id"`
	ReceivedAt   time.Time `json:"received_at"`
	Summary      Summary   `json:"summary"`
}

type Handler func(context.Context, Event)

type Summary struct {
	HistoryRecords  int      `json:"history_records"`
	MessagesAdded   int      `json:"messages_added"`
	MessagesDeleted int      `json:"messages_deleted"`
	LabelsAdded     int      `json:"labels_added"`
	LabelsRemoved   int      `json:"labels_removed"`
	MessageIDs      []string `json:"message_ids,omitempty"`
	LatestHistoryID uint64   `json:"latest_history_id,omitempty"`
}

type Watcher struct {
	cfg    gmailconfig.Config
	logger *log.Logger
	svc    *gmailapi.Service
	sub    *pubsub.Subscription

	mu         sync.RWMutex
	handlers   []Handler
	historyID  uint64
	expiration time.Time
}

func New(cfg gmailconfig.Config, svc *gmailapi.Service, client *pubsub.Client, logger *log.Logger) (*Watcher, error) {
	w := &Watcher{
		cfg:    cfg,
		logger: logger,
		svc:    svc,
	}

	if !cfg.Watch.Enabled {
		return w, nil
	}
	if cfg.Watch.ProjectID == "" || cfg.Watch.TopicName == "" || cfg.Watch.SubscriptionID == "" {
		return nil, fmt.Errorf("gmail watch requires project id, topic name, and subscription id")
	}
	if client == nil {
		return nil, fmt.Errorf("pubsub client is required when gmail watch is enabled")
	}

	w.sub = client.Subscription(cfg.Watch.SubscriptionID)
	return w, nil
}

func (w *Watcher) OnChange(handler Handler) {
	if handler == nil {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	w.handlers = append(w.handlers, handler)
}

func (w *Watcher) Run(ctx context.Context) error {
	if !w.cfg.Watch.Enabled {
		return nil
	}

	if err := w.renew(ctx); err != nil {
		return err
	}

	go w.renewLoop(ctx)

	err := w.sub.Receive(ctx, func(msgCtx context.Context, msg *pubsub.Message) {
		event, err := decodeEvent(msg.Data)
		if err != nil {
			w.logger.Printf("gmail watch decode failed: %v", err)
			msg.Ack()
			return
		}

		event.ReceivedAt = time.Now().UTC()
		prevHistoryID, ok := w.previousHistory(event.HistoryID)
		if !ok {
			w.logger.Printf("gmail watch pubsub event ignored; email=%s history_id=%d", event.EmailAddress, event.HistoryID)
			msg.Ack()
			return
		}

		w.logger.Printf("gmail watch pubsub event received; email=%s history_id=%d", event.EmailAddress, event.HistoryID)

		summary, err := w.loadHistory(msgCtx, prevHistoryID)
		if err != nil {
			w.logger.Printf("gmail watch history lookup failed after history_id=%d: %v", prevHistoryID, err)
		} else {
			event.Summary = summary
			w.logger.Printf(
				"gmail watch history summary; records=%d messages_added=%d messages_deleted=%d labels_added=%d labels_removed=%d latest_history_id=%d message_ids=%v",
				summary.HistoryRecords,
				summary.MessagesAdded,
				summary.MessagesDeleted,
				summary.LabelsAdded,
				summary.LabelsRemoved,
				summary.LatestHistoryID,
				summary.MessageIDs,
			)
		}

		msg.Ack()
		w.dispatch(msgCtx, event)
	})
	if err != nil && ctx.Err() == nil {
		return fmt.Errorf("receive gmail watch notifications: %w", err)
	}

	return nil
}

func (w *Watcher) renewLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if w.shouldRenew() {
				if err := w.renew(ctx); err != nil {
					w.logger.Printf("gmail watch renew failed: %v", err)
				}
			}
		}
	}
}

func (w *Watcher) shouldRenew() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()

	renewBefore := w.cfg.Watch.RenewBefore
	if renewBefore <= 0 {
		renewBefore = time.Hour
	}

	return w.expiration.IsZero() || time.Now().UTC().Add(renewBefore).After(w.expiration)
}

func (w *Watcher) renew(ctx context.Context) error {
	req := &gmailapi.WatchRequest{
		LabelFilterBehavior: "include",
		LabelIds:            []string{"DRAFT", "INBOX", "SPAM"},
		TopicName:           w.cfg.Watch.TopicName,
	}

	resp, err := w.svc.Users.Watch(w.cfg.User, req).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("register gmail watch: %w", err)
	}

	w.mu.Lock()
	w.historyID = resp.HistoryId
	w.expiration = time.UnixMilli(resp.Expiration).UTC()
	w.mu.Unlock()

	w.logger.Printf("gmail watch registered; history_id=%d expires_at=%s", resp.HistoryId, time.UnixMilli(resp.Expiration).UTC().Format(time.RFC3339))
	return nil
}

func (w *Watcher) previousHistory(next uint64) (uint64, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if next <= w.historyID {
		return 0, false
	}

	prev := w.historyID
	w.historyID = next
	return prev, prev != 0
}

func (w *Watcher) dispatch(ctx context.Context, event Event) {
	w.mu.RLock()
	handlers := append([]Handler(nil), w.handlers...)
	w.mu.RUnlock()

	for _, handler := range handlers {
		handler(ctx, event)
	}
}

type gmailNotification struct {
	EmailAddress string          `json:"emailAddress"`
	HistoryID    json.RawMessage `json:"historyId"`
}

func decodeEvent(data []byte) (Event, error) {
	var payload gmailNotification
	if err := json.Unmarshal(data, &payload); err != nil {
		return Event{}, fmt.Errorf("unmarshal gmail notification: %w", err)
	}

	historyID, err := parseHistoryID(payload.HistoryID)
	if err != nil {
		return Event{}, err
	}

	return Event{
		EmailAddress: payload.EmailAddress,
		HistoryID:    historyID,
	}, nil
}

func parseHistoryID(raw json.RawMessage) (uint64, error) {
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		historyID, parseErr := strconv.ParseUint(asString, 10, 64)
		if parseErr != nil {
			return 0, fmt.Errorf("parse history id string %q: %w", asString, parseErr)
		}
		return historyID, nil
	}

	var asNumber uint64
	if err := json.Unmarshal(raw, &asNumber); err == nil {
		return asNumber, nil
	}

	return 0, fmt.Errorf("parse history id: unsupported value %s", string(raw))
}

func (w *Watcher) loadHistory(ctx context.Context, startHistoryID uint64) (Summary, error) {
	var summary Summary

	call := w.svc.Users.History.List(w.cfg.User).
		Context(ctx).
		HistoryTypes("messageAdded", "messageDeleted", "labelAdded", "labelRemoved").
		StartHistoryId(startHistoryID).
		MaxResults(500)

	err := call.Pages(ctx, func(resp *gmailapi.ListHistoryResponse) error {
		summary.LatestHistoryID = resp.HistoryId
		for _, history := range resp.History {
			summary.HistoryRecords++
			summary.MessagesAdded += len(history.MessagesAdded)
			summary.MessagesDeleted += len(history.MessagesDeleted)
			summary.LabelsAdded += len(history.LabelsAdded)
			summary.LabelsRemoved += len(history.LabelsRemoved)

			for _, item := range history.MessagesAdded {
				summary.MessageIDs = appendMessageID(summary.MessageIDs, item.Message)
			}
			for _, item := range history.MessagesDeleted {
				summary.MessageIDs = appendMessageID(summary.MessageIDs, item.Message)
			}
			for _, item := range history.LabelsAdded {
				summary.MessageIDs = appendMessageID(summary.MessageIDs, item.Message)
			}
			for _, item := range history.LabelsRemoved {
				summary.MessageIDs = appendMessageID(summary.MessageIDs, item.Message)
			}
		}
		return nil
	})
	if err != nil {
		var apiErr *googleapi.Error
		if errors.As(err, &apiErr) && apiErr.Code == 404 {
			return summary, fmt.Errorf("gmail history expired or is too old, full resync required: %w", err)
		}
		return summary, err
	}

	return summary, nil
}

func appendMessageID(ids []string, message *gmailapi.Message) []string {
	if message == nil || message.Id == "" {
		return ids
	}

	for _, id := range ids {
		if id == message.Id {
			return ids
		}
	}

	return append(ids, message.Id)
}
