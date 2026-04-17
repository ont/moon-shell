package gogmoon

import (
	"reflect"
	"testing"
)

func TestSearchQueriesAddUnreadAndSubject(t *testing.T) {
	fetcher := &Fetcher{cfg: GogConfig{
		Subject:       "moon-shell hshp",
		UnreadOnly:    true,
		SearchSubject: true,
		SearchQueries: []string{"in:inbox", "in:spam"},
	}}

	got := fetcher.searchQueries()
	want := []string{
		`in:inbox is:unread subject:"moon-shell hshp"`,
		`in:spam is:unread subject:"moon-shell hshp"`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("searchQueries() = %#v, want %#v", got, want)
	}
}

func TestSearchQueriesDoNotDuplicateTerms(t *testing.T) {
	fetcher := &Fetcher{cfg: GogConfig{
		Subject:       "moon-shell",
		UnreadOnly:    true,
		SearchSubject: true,
		SearchQueries: []string{`in:spam is:unread subject:"moon-shell"`},
	}}

	got := fetcher.searchQueries()
	want := []string{`in:spam is:unread subject:"moon-shell"`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("searchQueries() = %#v, want %#v", got, want)
	}
}
