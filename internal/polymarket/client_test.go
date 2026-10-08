package polymarket

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

const page1 = `{"markets":[{
	"id":"559651","slug":"xi-out","question":"Xi Jinping out before 2027?",
	"active":true,"closed":false,"acceptingOrders":true,
	"createdAt":"2025-07-03T20:25:56.889606Z","endDate":"",
	"outcomes":"[\"Yes\", \"No\"]","outcomePrices":"[\"0.0265\", \"0.9735\"]",
	"volumeNum":14466588.8,"tags":[{"label":"Politics"},{"label":"China"}],
	"events":[{"slug":"xi-event","title":"Xi out?"}]
}],"next_cursor":"CUR2"}`

const page2 = `{"markets":[{"id":"2","question":"Second","outcomes":"","outcomePrices":""}],"next_cursor":""}`

func newTestClient(srv *httptest.Server) *Client {
	c := NewClient(srv.URL)
	c.retryDelay = time.Millisecond
	return c
}

func TestAllMarkets_FollowsCursorAndParses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/markets/keyset" || r.URL.Query().Get("limit") != "100" || r.URL.Query().Get("include_tag") != "true" || r.URL.Query().Get("closed") != "true" {
			t.Errorf("unexpected request %s", r.URL)
		}
		switch r.URL.Query().Get("after_cursor") {
		case "":
			w.Write([]byte(page1))
		case "CUR2":
			w.Write([]byte(page2))
		default:
			t.Errorf("unexpected cursor %q", r.URL.Query().Get("after_cursor"))
		}
	}))
	defer srv.Close()

	var got []Market
	err := newTestClient(srv).AllMarkets(context.Background(), true, func(page []Market) error {
		got = append(got, page...)
		return nil
	})
	if err != nil {
		t.Fatalf("AllMarkets: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d markets, want 2", len(got))
	}

	m := got[0]
	if m.ID != "559651" || m.EventSlug != "xi-event" || m.EventTitle != "Xi out?" {
		t.Errorf("ids/event = %q %q %q", m.ID, m.EventSlug, m.EventTitle)
	}
	if len(m.Outcomes) != 2 || m.Outcomes[0] != "Yes" {
		t.Errorf("outcomes = %v", m.Outcomes)
	}
	if len(m.OutcomePrices) != 2 || m.OutcomePrices[0] != 0.0265 || m.OutcomePrices[1] != 0.9735 {
		t.Errorf("outcome prices = %v", m.OutcomePrices)
	}
	if len(m.Tags) != 2 || m.Tags[1] != "China" {
		t.Errorf("tags = %v", m.Tags)
	}
	if m.CreatedAt == nil || m.CreatedAt.Year() != 2025 {
		t.Errorf("created_at = %v", m.CreatedAt)
	}
	if m.EndDate != nil {
		t.Errorf("empty endDate parsed as %v, want nil", m.EndDate)
	}
	if got[1].Outcomes != nil || got[1].OutcomePrices != nil {
		t.Errorf("empty outcome strings should stay nil: %+v", got[1])
	}
}

func TestMarketsPage_RetriesServerErrors(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(page2))
	}))
	defer srv.Close()

	markets, next, err := newTestClient(srv).MarketsPage(context.Background(), false, "")
	if err != nil {
		t.Fatalf("MarketsPage: %v", err)
	}
	if calls.Load() != 3 || len(markets) != 1 || next != "" {
		t.Fatalf("calls=%d markets=%d next=%q", calls.Load(), len(markets), next)
	}
}

func TestMarketsPage_GivesUpAfterMaxAttempts(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	if _, _, err := newTestClient(srv).MarketsPage(context.Background(), false, ""); err == nil {
		t.Fatal("want error after repeated 429")
	}
	if calls.Load() != maxAttempts {
		t.Fatalf("calls = %d, want %d", calls.Load(), maxAttempts)
	}
}

func TestMarketsPage_DoesNotRetryClientErrors(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "bad cursor", http.StatusBadRequest)
	}))
	defer srv.Close()

	if _, _, err := newTestClient(srv).MarketsPage(context.Background(), false, "x"); err == nil {
		t.Fatal("want error on 400")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1 (no retry on 4xx)", calls.Load())
	}
}

func TestMarketsPage_BadOutcomePricesIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"markets":[{"id":"9","outcomePrices":"[\"abc\"]"}],"next_cursor":""}`))
	}))
	defer srv.Close()

	if _, _, err := newTestClient(srv).MarketsPage(context.Background(), false, ""); err == nil {
		t.Fatal("want error for unparseable price")
	}
}
