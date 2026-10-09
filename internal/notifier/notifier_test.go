package notifier

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/rs/zerolog"

	"github.com/moonmouse11/polymarket-awesome-bot/internal/db"
	"github.com/moonmouse11/polymarket-awesome-bot/internal/polymarket"
)

type fakeQueue struct {
	pending  []db.AwesomeMarket
	notified []string
	msgIDs   []int
}

func (f *fakeQueue) PendingNotifications(context.Context, int64) ([]db.AwesomeMarket, error) {
	return f.pending, nil
}

func (f *fakeQueue) MarkNotified(_ context.Context, id string, _ time.Time, messageID int) error {
	f.notified = append(f.notified, id)
	f.msgIDs = append(f.msgIDs, messageID)
	return nil
}

type fakeSender struct {
	errs map[int]error // error for the n-th call (0-based)
	sent []string
}

// Send returns message ids 101, 102, ...
func (f *fakeSender) Send(_ context.Context, msg string) (int, error) {
	n := len(f.sent)
	f.sent = append(f.sent, msg)
	if err := f.errs[n]; err != nil {
		return 0, err
	}
	return 101 + n, nil
}

func market(id, question string) db.AwesomeMarket {
	return db.AwesomeMarket{Market: polymarket.Market{ID: id, Question: question, Slug: id}, Reason: db.ReasonTags}
}

func newTestNotifier(q Queue, s Sender) *Notifier {
	n := New(q, s, zerolog.Nop())
	n.sendEvery = 0
	return n
}

func TestSendBatch_SendsInOrderAndMarks(t *testing.T) {
	q := &fakeQueue{pending: []db.AwesomeMarket{market("1", "A?"), market("2", "B?")}}
	s := &fakeSender{}
	sent, err := newTestNotifier(q, s).sendBatch(context.Background())
	if err != nil || sent != 2 {
		t.Fatalf("sendBatch = %d, %v; want 2, nil", sent, err)
	}
	if strings.Join(q.notified, ",") != "1,2" {
		t.Errorf("notified = %v, want [1 2]", q.notified)
	}
	if len(q.msgIDs) != 2 || q.msgIDs[0] != 101 || q.msgIDs[1] != 102 {
		t.Errorf("message ids = %v, want [101 102]", q.msgIDs)
	}
}

func TestSendBatch_StopsOnErrorAndKeepsQueue(t *testing.T) {
	q := &fakeQueue{pending: []db.AwesomeMarket{market("1", "A?"), market("2", "B?"), market("3", "C?")}}
	s := &fakeSender{errs: map[int]error{1: errors.New("network down")}}
	sent, err := newTestNotifier(q, s).sendBatch(context.Background())
	if err == nil || sent != 1 {
		t.Fatalf("sendBatch = %d, %v; want 1 and an error", sent, err)
	}
	if strings.Join(q.notified, ",") != "1" {
		t.Errorf("notified = %v, want [1]: failed and later markets stay queued", q.notified)
	}
}

func TestSendBatch_SkipsRejectedMessage(t *testing.T) {
	q := &fakeQueue{pending: []db.AwesomeMarket{market("1", "A?"), market("2", "B?")}}
	s := &fakeSender{errs: map[int]error{0: fmt.Errorf("%w, can't parse entities", bot.ErrorBadRequest)}}
	if _, err := newTestNotifier(q, s).sendBatch(context.Background()); err != nil {
		t.Fatalf("sendBatch: %v", err)
	}
	if strings.Join(q.notified, ",") != "1,2" {
		t.Errorf("notified = %v, want [1 2]: a rejected message must not block the queue", q.notified)
	}
}

func TestFormat(t *testing.T) {
	end := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	base := polymarket.Market{
		Question:      "Will <aliens> & UFOs land?",
		Slug:          "aliens-land-2027",
		EventSlug:     "aliens-land",
		Outcomes:      []string{"Yes", "No"},
		OutcomePrices: []float64{0.12, 0.88},
		Volume:        48_200,
		EndDate:       &end,
		Tags:          []string{"Sports", "Middle East"},
	}
	head := "🛸 <b>Will &lt;aliens&gt; &amp; UFOs land?</b>\n\n" +
		"Yes 12% · No 88%\nVolume $48k · Ends Oct 31, 2026\n\n"
	tail := "\n\n#Sports #MiddleEast" +
		"\n\n<a href=\"https://polymarket.com/event/aliens-land/aliens-land-2027\">Open on Polymarket</a>"

	tests := []struct {
		name string
		mk   db.AwesomeMarket
		want string
	}{
		{"keyword overrides", db.AwesomeMarket{Market: base, Reason: db.ReasonWords, Words: []string{"alien", "ufo"}, ExcludedBy: "Sports"},
			head + "🔑 Passed: keyword «alien», «ufo»\n(overrides excluded tag «Sports»)" + tail},
		{"not filtered", db.AwesomeMarket{Market: base, Reason: db.ReasonTags},
			head + "✅ Passed: not filtered\n(none of the excluded tags)" + tail},
		{"not filtered with words", db.AwesomeMarket{Market: base, Reason: db.ReasonTags, Words: []string{"ufo"}},
			head + "✅ Passed: not filtered\n(none of the excluded tags)\nKeywords found: «ufo»" + tail},
	}
	for _, tt := range tests {
		if got := Format(tt.mk); got != tt.want {
			t.Errorf("%s: Format =\n%s\nwant\n%s", tt.name, got, tt.want)
		}
	}
}

func TestHashtags(t *testing.T) {
	got := hashtags([]string{"Iran Regime Rfr", "U.S. Politics", "AI & Tech", "2028", "Elon-Musk", "iran regime rfr", "Санкции"})
	want := "#IranRegimeRfr #USPolitics #AITech #ElonMusk #Санкции"
	if got != want {
		t.Errorf("hashtags = %q, want %q", got, want)
	}
}

// fakeTelegram answers Bot API calls like Telegram does.
func fakeTelegram(t *testing.T, sent *[]map[string]string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		// The library sends parameters as a multipart form.
		_ = r.ParseMultipartForm(1 << 20)
		params := map[string]string{}
		for k, v := range r.Form {
			params[k] = v[0]
		}
		switch method {
		case "getMe":
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"b"}}`))
		case "getChat":
			if params["chat_id"] != "@awesome_channel" {
				_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`))
				return
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":-1001,"type":"channel"}}`))
		case "sendMessage":
			*sent = append(*sent, params)
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1,"date":0,"chat":{"id":-1001,"type":"channel"}}}`))
		default:
			t.Errorf("unexpected method %s", method)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestTelegram_ChannelNameAndSend(t *testing.T) {
	var sent []map[string]string
	srv := fakeTelegram(t, &sent)
	ctx := context.Background()

	// A username without @ works: Telegram needs "@name".
	tg, err := NewTelegram("1:token", "awesome_channel", bot.WithServerURL(srv.URL))
	if err != nil {
		t.Fatalf("NewTelegram: %v", err)
	}
	if err := tg.Check(ctx); err != nil {
		t.Fatalf("Check: %v", err)
	}
	id, err := tg.Send(ctx, "<b>hi</b>")
	if err != nil || id != 1 {
		t.Fatalf("Send = %d, %v; want 1, nil", id, err)
	}
	if len(sent) != 1 || sent[0]["chat_id"] != "@awesome_channel" || sent[0]["parse_mode"] != "HTML" ||
		!strings.Contains(sent[0]["link_preview_options"], `"is_disabled":true`) {
		t.Errorf("sendMessage params = %v", sent)
	}

	// Telegram answered "chat not found": a config error, the bot must stop.
	wrong, err := NewTelegram("1:token", "@wrong", bot.WithServerURL(srv.URL))
	if err != nil {
		t.Fatalf("NewTelegram: %v", err)
	}
	if err := wrong.Check(ctx); err == nil || !IsConfigError(err) {
		t.Errorf("Check with an unknown channel = %v, want a config error", err)
	}

	// Telegram unreachable: not a config error, the bot keeps running.
	srv.Close()
	if err := tg.Check(ctx); err == nil || IsConfigError(err) {
		t.Errorf("Check with Telegram down = %v, want a non-config error", err)
	}
}
