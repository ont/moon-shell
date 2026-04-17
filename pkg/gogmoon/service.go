package gogmoon

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
)

type Status struct {
	LastAttempt    time.Time `json:"last_attempt,omitempty"`
	LastSuccess    time.Time `json:"last_success,omitempty"`
	Mailbox        string    `json:"mailbox,omitempty"`
	LastError      string    `json:"last_error,omitempty"`
	LastItemID     string    `json:"last_item_id,omitempty"`
	LastMatchCount int       `json:"last_match_count,omitempty"`
	LastSource     string    `json:"last_source,omitempty"`
	QueueDepth     int       `json:"queue_depth,omitempty"`
}

type queuedMessage struct {
	subject string
	message Message
}

type Service struct {
	logger        *log.Logger
	fetchInterval time.Duration
	workerCount   int
	fetcher       *Fetcher
	runner        *commandexec.Runner
	store         *commandexec.Store
	responder     *Responder
	gog           *Gog
	cfg           GogConfig

	fetchMu sync.Mutex
	queue   chan queuedMessage

	inFlightMu sync.Mutex
	inFlight   map[string]struct{}

	mu     sync.RWMutex
	status Status
}

func NewService(cfg Config, fetcher *Fetcher, runner *commandexec.Runner, store *commandexec.Store, responder *Responder, gog *Gog, logger *log.Logger) *Service {
	serviceCfg := cfg.ServiceConfig()
	service := &Service{
		logger:        logger,
		fetchInterval: serviceCfg.FetchInterval,
		workerCount:   serviceCfg.Workers,
		fetcher:       fetcher,
		runner:        runner,
		store:         store,
		responder:     responder,
		gog:           gog,
		cfg:           cfg.Gog,
		queue:         make(chan queuedMessage, serviceCfg.QueueSize),
		inFlight:      make(map[string]struct{}),
	}
	return service
}

func (s *Service) Run(ctx context.Context) {
	for i := 0; i < s.workerCount; i++ {
		go s.runWorker(ctx, i+1)
	}

	if err := s.fetch(ctx, "startup"); err != nil {
		s.logger.Printf("gog fetch failed: %v", err)
	}

	ticker := time.NewTicker(s.fetchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.fetch(ctx, "interval"); err != nil {
				s.logger.Printf("gog fetch failed: %v", err)
			}
		}
	}
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
		status.LastMatchCount = len(result.Items)
	})

	if !result.Found {
		s.logger.Printf("connected through gog account %s; no inbox or spam message with subject %q", result.Mailbox, result.Subject)
		return nil
	}

	last := result.Items[len(result.Items)-1]
	s.updateStatus(func(status *Status) {
		status.LastItemID = last.ID
	})

	s.logger.Printf("connected through gog account %s; found %d item(s) with subject=%q", result.Mailbox, len(result.Items), result.Subject)
	for _, message := range result.Items {
		s.logger.Printf("message %s labels=%v", message.ID, message.Labels)
		if err := s.enqueueMessage(ctx, result.Subject, message); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) enqueueMessage(ctx context.Context, subject string, message Message) error {
	record, found, err := s.store.GetRecord(ctx, message.ID)
	if err != nil {
		return err
	}
	if found && record.ResponseSentAt.Valid {
		if hasLabel(message.Labels, "UNREAD") {
			if err := s.gog.MarkRead(ctx, message.ID); err != nil {
				return err
			}
			s.logger.Printf("message %s already executed; marked as read", message.ID)
			return nil
		}
		s.logger.Printf("message %s already executed; skipping", message.ID)
		return nil
	}
	if !s.markInFlight(message.ID) {
		s.logger.Printf("message %s already queued or processing; skipping", message.ID)
		return nil
	}

	select {
	case s.queue <- queuedMessage{subject: subject, message: message}:
		s.updateStatus(func(status *Status) {
			status.QueueDepth = len(s.queue)
		})
		s.logger.Printf("message %s queued for gog processing", message.ID)
		return nil
	case <-ctx.Done():
		s.finishInFlight(message.ID)
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
			if err := s.processQueuedMessage(ctx, job); err != nil && ctx.Err() == nil {
				s.fail(err, fmt.Sprintf("worker:%d", workerID))
				s.logger.Printf("worker %d failed processing message %s: %v", workerID, job.message.ID, err)
			}
			s.finishInFlight(job.message.ID)
		}
	}
}

func (s *Service) processQueuedMessage(ctx context.Context, job queuedMessage) error {
	message := job.message
	record, found, err := s.store.GetRecord(ctx, message.ID)
	if err != nil {
		return err
	}

	workspace, err := PrepareWorkspace(ctx, s.cfg, s.gog, message)
	if err != nil {
		return err
	}

	if found {
		s.logger.Printf("message %s already executed; retrying response send", message.ID)
		return s.sendResponse(ctx, workspace, message, commandexec.Result{
			Command:  record.Command,
			Stdout:   record.Stdout,
			Stderr:   record.Stderr,
			ExitCode: record.ExitCode,
		})
	}

	command := ExtractCommand(message.Body)
	if command == "" {
		s.logger.Printf("message %s does not contain an executable command after normalization; skipping", message.ID)
		return nil
	}

	execResult, err := s.runner.ExecuteIn(ctx, command, workspace.Dir)
	if err != nil {
		return err
	}

	executionRecord := commandexec.Record{
		MessageID:  message.ID,
		FromAddr:   message.From,
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

	return s.sendResponse(ctx, workspace, message, execResult)
}

func (s *Service) sendResponse(ctx context.Context, workspace Workspace, message Message, result commandexec.Result) error {
	if err := s.responder.Send(ctx, workspace, message, result); err != nil {
		return fmt.Errorf("send response for message %s: %w", message.ID, err)
	}
	if err := s.store.MarkResponseSent(ctx, message.ID, time.Now().UTC()); err != nil {
		return err
	}
	if err := s.gog.MarkRead(ctx, message.ID); err != nil {
		return err
	}
	s.logger.Printf("message %s executed through gog and response sent; exit_code=%d workspace=%s", message.ID, result.ExitCode, workspace.Dir)
	return nil
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

func ExtractCommand(body string) string {
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

	replyHeaders := []string{"from:", "to:", "subject:", "date:", "sent:", "cc:", "кому:", "тема:", "дата:", "от:"}
	for _, prefix := range replyHeaders {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return strings.Contains(lower, "original message")
}

func isSeparatorLine(line string) bool {
	if len(line) < 3 {
		return false
	}
	for _, char := range line {
		if char != '-' && char != '_' && char != '=' {
			return false
		}
	}
	return true
}

func hasLabel(labels []string, target string) bool {
	for _, label := range labels {
		if label == target {
			return true
		}
	}
	return false
}
