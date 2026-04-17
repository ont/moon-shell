package gmail

import (
	"context"
	"fmt"
	htmlstd "html"
	"log"
	"strings"
	"sync"
	"time"

	htmlnode "golang.org/x/net/html"
	"moon-shell/pkg/commandexec"
	gmailconfig "moon-shell/pkg/gmail/config"
	"moon-shell/pkg/gmail/fetcher"
	"moon-shell/pkg/gmail/responder"
	"moon-shell/pkg/gmail/watcher"
)

type Status struct {
	LastAttempt    time.Time `json:"last_attempt,omitempty"`
	LastSuccess    time.Time `json:"last_success,omitempty"`
	Mailbox        string    `json:"mailbox,omitempty"`
	LastError      string    `json:"last_error,omitempty"`
	LastItemID     string    `json:"last_item_id,omitempty"`
	LastItemKind   string    `json:"last_item_kind,omitempty"`
	LastMatchCount int       `json:"last_match_count,omitempty"`
	LastSource     string    `json:"last_source,omitempty"`
	QueueDepth     int       `json:"queue_depth,omitempty"`
}

type queuedItem struct {
	subject string
	item    fetcher.Item
}

type Service struct {
	logger        *log.Logger
	fetchInterval time.Duration
	workerCount   int
	fetcher       *fetcher.Fetcher
	runner        *commandexec.Runner
	store         *commandexec.Store
	responder     *responder.Responder
	watcher       *watcher.Watcher

	fetchMu sync.Mutex
	queue   chan queuedItem

	inFlightMu sync.Mutex
	inFlight   map[string]struct{}

	mu     sync.RWMutex
	status Status
}

func NewService(cfg gmailconfig.ServiceConfig, fetcher *fetcher.Fetcher, runner *commandexec.Runner, store *commandexec.Store, responder *responder.Responder, mailWatcher *watcher.Watcher, logger *log.Logger) *Service {
	workers := cfg.Workers
	if workers <= 0 {
		workers = 1
	}
	queueSize := cfg.QueueSize
	if queueSize <= 0 {
		queueSize = workers
	}

	service := &Service{
		logger:        logger,
		fetchInterval: cfg.FetchInterval,
		workerCount:   workers,
		fetcher:       fetcher,
		runner:        runner,
		store:         store,
		responder:     responder,
		watcher:       mailWatcher,
		queue:         make(chan queuedItem, queueSize),
		inFlight:      make(map[string]struct{}),
	}

	mailWatcher.OnChange(func(ctx context.Context, event watcher.Event) {
		if err := service.Poll(ctx, event); err != nil {
			service.logger.Printf("gmail watch poll failed: %v", err)
		}
	})

	return service
}

func (s *Service) Run(ctx context.Context) {
	for i := 0; i < s.workerCount; i++ {
		go s.runWorker(ctx, i+1)
	}

	go func() {
		if err := s.watcher.Run(ctx); err != nil && ctx.Err() == nil {
			s.fail(err, "watch")
			s.logger.Printf("gmail watcher failed: %v", err)
		}
	}()

	interval := s.fetchInterval
	if interval <= 0 {
		interval = time.Minute
	}

	if err := s.fetch(ctx, "startup"); err != nil {
		s.logger.Printf("gmail fetch failed: %v", err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.fetch(ctx, "interval"); err != nil {
				s.logger.Printf("gmail fetch failed: %v", err)
			}
		}
	}
}

func (s *Service) Poll(ctx context.Context, event watcher.Event) error {
	source := "watch"
	if event.EmailAddress != "" {
		source = "watch:" + event.EmailAddress
	}

	return s.fetch(ctx, source)
}

func (s *Service) Snapshot() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.status
}

func (s *Service) fetch(ctx context.Context, source string) error {
	s.fetchMu.Lock()
	defer s.fetchMu.Unlock()

	s.updateStatus(func(status *Status) {
		status.LastAttempt = time.Now().UTC()
		status.LastError = ""
		status.LastSource = source
	})

	result, err := s.fetcher.Fetch(ctx)
	if err != nil {
		s.fail(err, source)
		return err
	}

	s.updateStatus(func(status *Status) {
		status.LastSuccess = time.Now().UTC()
		status.Mailbox = result.Mailbox
		status.LastItemID = ""
		status.LastItemKind = ""
		status.LastMatchCount = len(result.Items)
	})

	if !result.Found {
		s.logger.Printf("connected to mailbox %s; no draft, inbox, or spam message with subject %q", result.Mailbox, result.Subject)
		return nil
	}

	last := result.Items[len(result.Items)-1]
	s.updateStatus(func(status *Status) {
		status.LastItemID = last.ID
		status.LastItemKind = last.Kind
	})

	s.logger.Printf("connected to mailbox %s; found %d item(s) with subject=%q", result.Mailbox, len(result.Items), result.Subject)
	for _, item := range result.Items {
		s.logger.Printf("item %s %s", item.Kind, item.ID)
		if item.Body == "" {
			s.logger.Printf("%s body is empty", item.Kind)
		} else {
			s.logger.Printf("%s body:\n%s", item.Kind, item.Body)
		}
		if err := s.enqueueItem(ctx, result.Subject, item); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) enqueueItem(ctx context.Context, subject string, item fetcher.Item) error {
	if item.Kind == "draft" {
		return nil
	}

	record, found, err := s.store.GetRecord(ctx, item.ID)
	if err != nil {
		return err
	}
	if found && record.ResponseSentAt.Valid {
		s.logger.Printf("message %s already executed; skipping", item.ID)
		return nil
	}
	if !s.markInFlight(item.ID) {
		s.logger.Printf("message %s already queued or processing; skipping", item.ID)
		return nil
	}

	select {
	case s.queue <- queuedItem{subject: subject, item: item}:
		s.updateStatus(func(status *Status) {
			status.QueueDepth = len(s.queue)
		})
		s.logger.Printf("message %s queued for processing", item.ID)
		return nil
	case <-ctx.Done():
		s.finishInFlight(item.ID)
		return ctx.Err()
	}
}

func (s *Service) runWorker(ctx context.Context, workerID int) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-s.queue:
			s.updateStatus(func(status *Status) {
				status.QueueDepth = len(s.queue)
			})
			if err := s.processQueuedItem(ctx, job); err != nil && ctx.Err() == nil {
				s.fail(err, fmt.Sprintf("worker:%d", workerID))
				s.logger.Printf("worker %d failed processing message %s: %v", workerID, job.item.ID, err)
			}
			s.finishInFlight(job.item.ID)
		}
	}
}

func (s *Service) processQueuedItem(ctx context.Context, job queuedItem) error {
	item := job.item
	record, found, err := s.store.GetRecord(ctx, item.ID)
	if err != nil {
		return err
	}

	if found {
		s.logger.Printf("message %s already executed; retrying response send", item.ID)
		return s.sendResponse(ctx, item, commandexec.Result{
			Command:  record.Command,
			Stdout:   record.Stdout,
			Stderr:   record.Stderr,
			ExitCode: record.ExitCode,
		})
	}

	command := extractCommand(item.Body)
	if command == "" {
		s.logger.Printf("message %s does not contain an executable command after normalization; skipping", item.ID)
		return nil
	}

	execResult, err := s.runner.Execute(ctx, command)
	if err != nil {
		return err
	}

	executionRecord := commandexec.Record{
		MessageID:  item.ID,
		FromAddr:   item.From,
		Subject:    job.subject,
		Command:    execResult.Command,
		Stdout:     execResult.Stdout,
		Stderr:     execResult.Stderr,
		ExitCode:   execResult.ExitCode,
		ExecutedAt: time.Now().UTC(),
	}
	if err := s.store.SaveExecution(ctx, executionRecord); err != nil {
		return err
	}

	return s.sendResponse(ctx, item, execResult)
}

func (s *Service) markInFlight(messageID string) bool {
	s.inFlightMu.Lock()
	defer s.inFlightMu.Unlock()

	if _, exists := s.inFlight[messageID]; exists {
		return false
	}

	s.inFlight[messageID] = struct{}{}
	return true
}

func (s *Service) finishInFlight(messageID string) {
	s.inFlightMu.Lock()
	delete(s.inFlight, messageID)
	s.inFlightMu.Unlock()

	s.updateStatus(func(status *Status) {
		status.QueueDepth = len(s.queue)
	})
}

func (s *Service) sendResponse(ctx context.Context, item fetcher.Item, result commandexec.Result) error {
	if err := s.responder.Send(ctx, item, result); err != nil {
		return fmt.Errorf("send response for message %s: %w", item.ID, err)
	}
	if err := s.store.MarkResponseSent(ctx, item.ID, time.Now().UTC()); err != nil {
		return err
	}

	s.logger.Printf("message %s executed and response sent; exit_code=%d", item.ID, result.ExitCode)
	return nil
}

func (s *Service) fail(err error, source string) {
	s.updateStatus(func(status *Status) {
		status.LastError = err.Error()
		status.LastSource = source
	})
}

func (s *Service) updateStatus(update func(*Status)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	update(&s.status)
}

func extractCommand(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}

	text := body
	if containsHTML(text) {
		if extracted, err := htmlToText(text); err == nil && strings.TrimSpace(extracted) != "" {
			text = extracted
		}
	}

	text = htmlstd.UnescapeString(text)
	return trimQuotedReply(text)
}

func htmlToText(input string) (string, error) {
	nodes, err := htmlnode.ParseFragment(strings.NewReader(input), nil)
	if err != nil {
		return "", err
	}

	var parts []string
	for _, node := range nodes {
		collectText(node, &parts)
	}

	return strings.Join(parts, "\n"), nil
}

func containsHTML(value string) bool {
	return strings.Contains(value, "<") && strings.Contains(value, ">")
}

func trimQuotedReply(value string) string {
	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	var kept []string

	for _, line := range lines {
		if isQuotedReplyBoundary(line) && len(kept) > 0 {
			break
		}
		kept = append(kept, line)
	}

	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func isQuotedReplyBoundary(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}

	if strings.HasPrefix(trimmed, ">") {
		return true
	}

	if isSeparatorLine(trimmed) {
		return true
	}

	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "on ") && strings.Contains(lower, " wrote:") {
		return true
	}

	replyHeaders := []string{
		"from:",
		"to:",
		"subject:",
		"date:",
		"sent:",
		"cc:",
		"кому:",
		"тема:",
		"дата:",
		"от:",
	}
	for _, prefix := range replyHeaders {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}

	return strings.Contains(lower, "original message")
}

func isSeparatorLine(value string) bool {
	if len(value) < 5 {
		return false
	}

	for _, r := range value {
		switch r {
		case '-', '_', '*', '=':
		default:
			return false
		}
	}

	return true
}

func collectText(node *htmlnode.Node, parts *[]string) {
	if node.Type == htmlnode.TextNode {
		text := strings.TrimSpace(node.Data)
		if text != "" {
			*parts = append(*parts, text)
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		collectText(child, parts)
	}
}
