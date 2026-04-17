package gmail

import (
	"testing"

	"moon-shell/pkg/gmail/fetcher"
)

func TestExtractCommandStripsQuotedReply(t *testing.T) {
	body := `<div>apk add curl</div><div><br /></div><div><br /></div><div>----------------</div><div>Кому: ontrif@yandex.ru</div><blockquote><p>moon-shell execution response</p></blockquote>`

	got := extractCommand(body)
	want := "apk add curl"
	if got != want {
		t.Fatalf("extractCommand() = %q, want %q", got, want)
	}
}

func TestExtractCommandKeepsMultiLineCommand(t *testing.T) {
	body := "printf 'hello\\n'\npwd"

	got := extractCommand(body)
	if got != body {
		t.Fatalf("extractCommand() = %q, want %q", got, body)
	}
}

func TestMarkInFlight(t *testing.T) {
	service := &Service{queue: make(chan queuedItem, 1), inFlight: make(map[string]struct{})}

	if !service.markInFlight("msg-1") {
		t.Fatalf("first markInFlight() = false, want true")
	}
	if service.markInFlight("msg-1") {
		t.Fatalf("second markInFlight() = true, want false")
	}

	service.finishInFlight("msg-1")

	if !service.markInFlight("msg-1") {
		t.Fatalf("markInFlight() after finish = false, want true")
	}
}

func TestFinishInFlightUpdatesQueueDepth(t *testing.T) {
	service := &Service{
		queue:    make(chan queuedItem, 2),
		inFlight: make(map[string]struct{}),
	}
	service.queue <- queuedItem{subject: "moon-shell", item: fetcher.Item{ID: "msg-2"}}
	service.markInFlight("msg-1")

	service.finishInFlight("msg-1")

	if got := service.Snapshot().QueueDepth; got != 1 {
		t.Fatalf("QueueDepth = %d, want 1", got)
	}
}
