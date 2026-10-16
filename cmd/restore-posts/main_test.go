package main

import (
	"testing"
	"time"
)

func TestParseExport(t *testing.T) {
	data := []byte(`{"name": "Channel", "messages": [
		{"id": 1, "type": "service", "date_unixtime": "1700000000", "text_entities": []},
		{"id": 2, "type": "message", "date_unixtime": "1700000060", "text_entities": [
			{"type": "bold", "text": "Will aliens land?"},
			{"type": "text_link", "text": "Open on Polymarket", "href": "https://polymarket.com/event/aliens/will-aliens-land"}]},
		{"id": 3, "type": "message", "date_unixtime": "1700000120", "text_entities": [
			{"type": "text_link", "text": "Open on Polymarket", "href": "https://polymarket.com/market/ufo-2027"}]},
		{"id": 4, "type": "message", "date_unixtime": "1700000180", "text_entities": [
			{"type": "text_link", "text": "event only", "href": "https://polymarket.com/event/aliens"},
			{"type": "text_link", "text": "other", "href": "https://example.com/event/a/b"}]},
		{"id": 5, "type": "message", "date_unixtime": "1700000240", "text_entities": [{"type": "plain", "text": "hello"}]}
	]}`)
	posts, err := parseExport(data)
	if err != nil {
		t.Fatalf("parseExport: %v", err)
	}
	want := []post{
		{MessageID: 2, At: time.Unix(1700000060, 0).UTC(), Slug: "will-aliens-land"},
		{MessageID: 3, At: time.Unix(1700000120, 0).UTC(), Slug: "ufo-2027"},
	}
	if len(posts) != len(want) {
		t.Fatalf("posts = %+v, want %+v", posts, want)
	}
	for i := range want {
		if posts[i] != want[i] {
			t.Errorf("posts[%d] = %+v, want %+v", i, posts[i], want[i])
		}
	}
}

func TestParseExport_BadJSON(t *testing.T) {
	if _, err := parseExport([]byte(`not json`)); err == nil {
		t.Fatal("want error")
	}
}
