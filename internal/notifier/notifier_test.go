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
	"github.com/moonmouse11/polymarket-awesome-bot/internal/health"
	"github.com/moonmouse11/polymarket-awesome-bot/internal/polymarket"
)

type fakeQueue struct {
	pending  []db.AwesomeMarket
	notified []string
	msgIDs   []int
	edits    []db.AwesomeMarket
	edited   []string
	deletes  []db.PendingDelete
	deleted  []string
	failed   []string

	pendingErr error // returned by every Pending* call
	markErr    error // returned by every Mark* call
}

func (f *fakeQueue) PendingDeletes(context.Context, int64) ([]db.PendingDelete, error) {
	return f.deletes, f.pendingErr
}

func (f *fakeQueue) MarkDeleted(_ context.Context, ids []string, _ time.Time) error {
	f.deleted = append(f.deleted, ids...)
	return f.markErr
}

func (f *fakeQueue) MarkDeleteFailed(_ context.Context, id, _ string) error {
	f.failed = append(f.failed, id)
	return f.markErr
}

func (f *fakeQueue) PendingEdits(context.Context, int64) ([]db.AwesomeMarket, error) {
	return f.edits, f.pendingErr
}

func (f *fakeQueue) MarkEdited(_ context.Context, id string) error {
	f.edited = append(f.edited, id)
	return f.markErr
}

func (f *fakeQueue) PendingNotifications(context.Context, int64) ([]db.AwesomeMarket, error) {
	return f.pending, f.pendingErr
}

func (f *fakeQueue) MarkNotified(_ context.Context, id string, _ time.Time, messageID int) error {
	f.notified = append(f.notified, id)
	f.msgIDs = append(f.msgIDs, messageID)
	return f.markErr
}

type fakeSender struct {
	errs   map[int]error // error for the n-th Send (0-based)
	sent   []string
	edits  []int // edited message ids
	editEr error

	batches   [][]int       // deleteMessages calls
	batchErr  error         // error of every deleteMessages call
	singles   []int         // deleteMessage calls
	singleErr map[int]error // error of deleteMessage per message id
}

func (f *fakeSender) Delete(_ context.Context, ids []int) error {
	f.batches = append(f.batches, ids)
	return f.batchErr
}

func (f *fakeSender) DeleteOne(_ context.Context, id int) error {
	f.singles = append(f.singles, id)
	return f.singleErr[id]
}

func (f *fakeSender) Edit(_ context.Context, messageID int, _ string) error {
	f.edits = append(f.edits, messageID)
	return f.editEr
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
	return db.AwesomeMarket{Market: polymarket.Market{ID: id, Question: question, Slug: id}, Reason: db.ReasonDefault}
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

func TestDeleteBatch_OneCallForAll(t *testing.T) {
	q := &fakeQueue{deletes: []db.PendingDelete{{ID: "1", MessageID: 11}, {ID: "2", MessageID: 12}}}
	s := &fakeSender{}
	done, err := newTestNotifier(q, s).deleteBatch(context.Background())
	if err != nil || done != 2 {
		t.Fatalf("deleteBatch = %d, %v; want 2, nil", done, err)
	}
	if len(s.batches) != 1 || fmt.Sprint(s.batches[0]) != "[11 12]" || len(s.singles) != 0 {
		t.Errorf("batches %v, singles %v; want one call with [11 12]", s.batches, s.singles)
	}
	if strings.Join(q.deleted, ",") != "1,2" {
		t.Errorf("deleted = %v, want [1 2]", q.deleted)
	}
}

func TestDeleteBatch_RejectedBatchFallsBackToSingles(t *testing.T) {
	q := &fakeQueue{deletes: []db.PendingDelete{{ID: "old", MessageID: 11}, {ID: "new", MessageID: 12}}}
	tooOld := fmt.Errorf("%w, message can't be deleted", bot.ErrorBadRequest)
	s := &fakeSender{batchErr: tooOld, singleErr: map[int]error{11: tooOld}}
	done, err := newTestNotifier(q, s).deleteBatch(context.Background())
	if err != nil || done != 2 {
		t.Fatalf("deleteBatch = %d, %v; want 2 (both left the queue), nil", done, err)
	}
	if strings.Join(q.deleted, ",") != "new" || strings.Join(q.failed, ",") != "old" {
		t.Errorf("deleted %v, failed %v; want [new], [old]", q.deleted, q.failed)
	}
}

func TestDeleteBatch_TelegramDownKeepsQueue(t *testing.T) {
	q := &fakeQueue{deletes: []db.PendingDelete{{ID: "1", MessageID: 11}}}
	s := &fakeSender{batchErr: errors.New("network down")}
	if _, err := newTestNotifier(q, s).deleteBatch(context.Background()); err == nil || len(q.deleted)+len(q.failed) != 0 {
		t.Errorf("err = %v, deleted %v, failed %v; want an error and the queue untouched", err, q.deleted, q.failed)
	}
}

func TestStep_DeletesThenSendsThenEdits(t *testing.T) {
	m := market("2", "B?")
	m.MessageID = 21
	q := &fakeQueue{
		deletes: []db.PendingDelete{{ID: "1", MessageID: 11}},
		pending: []db.AwesomeMarket{market("3", "C?")},
		edits:   []db.AwesomeMarket{m},
	}
	s := &fakeSender{}
	n := newTestNotifier(q, s)

	if _, err := n.step(context.Background()); err != nil {
		t.Fatalf("step: %v", err)
	}
	if len(s.batches) != 1 || len(s.sent) != 0 || len(s.edits) != 0 {
		t.Errorf("first step: deletes %v, sends %d, edits %v; want only the delete", s.batches, len(s.sent), s.edits)
	}

	q.deletes = nil
	if _, err := n.step(context.Background()); err != nil || len(s.sent) != 1 || len(s.edits) != 0 {
		t.Errorf("second step: err %v, sends %d, edits %v; want the send", err, len(s.sent), s.edits)
	}
	q.pending = nil
	if _, err := n.step(context.Background()); err != nil || len(s.edits) != 1 {
		t.Errorf("third step: err %v, edits %v; want the edit", err, s.edits)
	}
}

func TestEditBatch_EditsAndSkipsDeleted(t *testing.T) {
	m1, m2 := market("1", "A?"), market("2", "B?")
	m1.MessageID, m2.MessageID = 11, 12
	q := &fakeQueue{edits: []db.AwesomeMarket{m1, m2}}
	s := &fakeSender{}
	edited, err := newTestNotifier(q, s).editBatch(context.Background())
	if err != nil || edited != 2 {
		t.Fatalf("editBatch = %d, %v; want 2, nil", edited, err)
	}
	if len(s.edits) != 2 || s.edits[0] != 11 || s.edits[1] != 12 || strings.Join(q.edited, ",") != "1,2" {
		t.Errorf("edited messages %v, markets %v; want [11 12], [1 2]", s.edits, q.edited)
	}

	// A message deleted from the channel: skipped, not retried forever.
	q = &fakeQueue{edits: []db.AwesomeMarket{m1}}
	s = &fakeSender{editEr: fmt.Errorf("%w, message to edit not found", bot.ErrorBadRequest)}
	if _, err := newTestNotifier(q, s).editBatch(context.Background()); err != nil || len(q.edited) != 1 {
		t.Errorf("editBatch = %v, edited %v; want nil, [1]", err, q.edited)
	}

	// Telegram down: the market stays in the edit queue.
	q = &fakeQueue{edits: []db.AwesomeMarket{m1}}
	s = &fakeSender{editEr: errors.New("network down")}
	if _, err := newTestNotifier(q, s).editBatch(context.Background()); err == nil || len(q.edited) != 0 {
		t.Errorf("editBatch = %v, edited %v; want an error and nothing marked", err, q.edited)
	}
}

func TestPace_SpacesCallsAcrossBatches(t *testing.T) {
	n := newTestNotifier(&fakeQueue{}, &fakeSender{})
	n.sendEvery = 50 * time.Millisecond
	start := time.Now()
	for range 3 {
		if err := n.pace(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if took := time.Since(start); took < 100*time.Millisecond {
		t.Errorf("3 calls took %v, want ≥ 2 × sendEvery", took)
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
	head := "<b>Will &lt;aliens&gt; &amp; UFOs land?</b>\n\n" +
		"Yes 12% · No 88%\nVolume $48k · Ends Oct 31, 2026\n\n"
	tail := "\n\n#Sports #MiddleEast" +
		"\n\n<a href=\"https://polymarket.com/event/aliens-land/aliens-land-2027\">Open on Polymarket</a>"

	tests := []struct {
		name string
		mk   db.AwesomeMarket
		want string
	}{
		{"allow word overrides a deny tag", db.AwesomeMarket{Market: base, Reason: db.ReasonAllow,
			Matches: db.Matches{AllowWords: []string{"alien", "ufo"}, DenyTags: []string{"Sports"}}},
			head + "🔑 Allowed by word «alien», «ufo»\n(overrides tag «Sports»)" + tail},
		{"allow tag and word", db.AwesomeMarket{Market: base, Reason: db.ReasonAllow,
			Matches: db.Matches{AllowTags: []string{"AI"}, AllowWords: []string{"ufo"}}},
			head + "🔑 Allowed by tag «AI», word «ufo»" + tail},
		{"not filtered", db.AwesomeMarket{Market: base, Reason: db.ReasonDefault},
			head + "✅ No deny rule matched" + tail},
		{"filtered out since posted", db.AwesomeMarket{Market: base, Reason: db.ReasonDeny,
			Matches: db.Matches{DenyTags: []string{"Sports"}, DenyWords: []string{"goalscorer"}}},
			head + "⛔ Denied by tag «Sports», word «goalscorer»" + tail},
	}
	for _, tt := range tests {
		if got := Format(tt.mk); got != tt.want {
			t.Errorf("%s: Format =\n%s\nwant\n%s", tt.name, got, tt.want)
		}
	}
}

func TestFormat_Description(t *testing.T) {
	mk := db.AwesomeMarket{Market: polymarket.Market{Question: "Q?", Slug: "q",
		Description: "  Resolves <Yes> if & only if…\n\nSource: AP.  "}, Reason: db.ReasonDefault}
	want := "<b>Q?</b>\n\n<blockquote expandable>Resolves &lt;Yes&gt; if &amp; only if…\n\nSource: AP.</blockquote>\n\n✅ No deny rule matched"
	if got := Format(mk); !strings.HasPrefix(got, want) {
		t.Errorf("Format =\n%s\nwant prefix\n%s", got, want)
	}

	// The longest description on Polymarket is ~6400 characters: it is cut
	// so that the whole message fits Telegram's 4096.
	mk.Description = strings.Repeat("я", 6400)
	got := Format(mk)
	if n := len([]rune(got)); n > 4096 || !strings.Contains(got, "…</blockquote>") {
		t.Errorf("long description: message of %d characters; want ≤ 4096 with the description cut", n)
	}
	if n := strings.Count(got, "я"); n < 3800 {
		t.Errorf("long description: %d characters kept, want most of the 4000 budget", n)
	}
	if got := description(strings.Repeat("a", 500), 100); len([]rune(got)) != 100+len("<blockquote expandable></blockquote>") {
		t.Errorf("description cut to 100: %q", got)
	}
	if description("   ", 1000) != "" || description("text", 50) != "" {
		t.Error("blank description or no room: want no section")
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

func TestTelegram_Delete(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(1 << 20)
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		calls = append(calls, method+" "+r.FormValue("message_ids")+r.FormValue("message_id"))
		switch r.FormValue("message_id") {
		case "404":
			_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: message to delete not found"}`))
		case "48":
			_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: message can't be deleted"}`))
		default:
			_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
		}
	}))
	defer srv.Close()
	tg, err := NewTelegram("1:token", "@c", bot.WithServerURL(srv.URL))
	if err != nil {
		t.Fatalf("NewTelegram: %v", err)
	}
	ctx := context.Background()

	if err := tg.Delete(ctx, []int{11, 12}); err != nil {
		t.Errorf("Delete: %v", err)
	}
	if err := tg.DeleteOne(ctx, 404); err != nil {
		t.Errorf("DeleteOne of a missing message = %v, want nil (already gone)", err)
	}
	if err := tg.DeleteOne(ctx, 48); err == nil || !isBadMessage(err) {
		t.Errorf("DeleteOne refused = %v, want a 400", err)
	}
	if len(calls) != 3 || calls[0] != "deleteMessages [11,12]" || calls[1] != "deleteMessage 404" {
		t.Errorf("calls = %q", calls)
	}
}

type fakeReporter struct {
	reports []error
	limited []time.Duration
}

func (f *fakeReporter) Report(name string, err error) {
	if name == health.Telegram {
		f.reports = append(f.reports, err)
	}
}

func (f *fakeReporter) RateLimited(d time.Duration) { f.limited = append(f.limited, d) }

func TestObserve_ReportsTelegramHealth(t *testing.T) {
	r := &fakeReporter{}
	n := newTestNotifier(&fakeQueue{}, &fakeSender{}).WithHealth(r)
	down := errors.New("connection refused")

	n.observe(nil)
	n.observe(fmt.Errorf("%w: chat not found", bot.ErrorBadRequest)) // rejected message: Telegram is up
	n.observe(down)
	n.observe(&bot.TooManyRequestsError{RetryAfter: 3})

	if len(r.reports) != 3 || r.reports[0] != nil || r.reports[1] != nil || r.reports[2] != down {
		t.Errorf("reports = %v, want [nil nil %v]", r.reports, down)
	}
	if len(r.limited) != 1 || r.limited[0] != 3*time.Second {
		t.Errorf("rate limits = %v, want [3s]", r.limited)
	}
}

// stopQueue cancels the context once `after` markets are announced.
type stopQueue struct {
	*fakeQueue
	after  int
	cancel context.CancelFunc
}

func (q *stopQueue) MarkNotified(ctx context.Context, id string, at time.Time, messageID int) error {
	err := q.fakeQueue.MarkNotified(ctx, id, at, messageID)
	if len(q.notified) == q.after {
		q.cancel()
	}
	return err
}

func TestRun_RetriesAfterFailureAndStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	q := &stopQueue{fakeQueue: &fakeQueue{pending: []db.AwesomeMarket{market("1", "A?"), market("2", "B?")}}, after: 2, cancel: cancel}
	s := &fakeSender{errs: map[int]error{0: errors.New("telegram down")}}
	n := newTestNotifier(q, s)
	n.idleWait = time.Millisecond

	done := make(chan error, 1)
	go func() { done <- n.Run(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("Run did not stop")
	}
	// The first send failed and was retried: 3 sends, 2 announcements.
	if len(s.sent) != 3 || len(q.notified) != 2 || q.msgIDs[0] != 102 {
		t.Errorf("sent %d, notified %v with ids %v; want 3 sends, [1 2] from id 102", len(s.sent), q.notified, q.msgIDs)
	}
}

func TestStep_QueueErrors(t *testing.T) {
	dbDown := errors.New("mongo down")
	ctx := context.Background()

	if _, err := newTestNotifier(&fakeQueue{pendingErr: dbDown}, &fakeSender{}).step(ctx); !errors.Is(err, dbDown) {
		t.Errorf("step with Pending* failing = %v, want %v", err, dbDown)
	}
	for name, q := range map[string]*fakeQueue{
		"delete": {deletes: []db.PendingDelete{{ID: "1", MessageID: 11}}, markErr: dbDown},
		"send":   {pending: []db.AwesomeMarket{market("1", "A?")}, markErr: dbDown},
		"edit":   {edits: []db.AwesomeMarket{market("1", "A?")}, markErr: dbDown},
	} {
		if _, err := newTestNotifier(q, &fakeSender{}).step(ctx); !errors.Is(err, dbDown) {
			t.Errorf("%s with Mark* failing = %v, want %v", name, err, dbDown)
		}
	}
}

func TestBatches_TelegramDownStopsBatch(t *testing.T) {
	down := errors.New("telegram down")
	ctx := context.Background()

	edit := &fakeQueue{edits: []db.AwesomeMarket{market("1", "A?")}}
	if _, err := newTestNotifier(edit, &fakeSender{editEr: down}).editBatch(ctx); !errors.Is(err, down) || len(edit.edited) != 0 {
		t.Errorf("editBatch = %v, edited %v; want the error and nothing marked", err, edit.edited)
	}

	// The batch delete is rejected, then a single delete fails with an outage.
	del := &fakeQueue{deletes: []db.PendingDelete{{ID: "1", MessageID: 11}}}
	s := &fakeSender{batchErr: bot.ErrorBadRequest, singleErr: map[int]error{11: down}}
	if _, err := newTestNotifier(del, s).deleteBatch(ctx); !errors.Is(err, down) || len(del.deleted)+len(del.failed) != 0 {
		t.Errorf("deleteBatch = %v, deleted %v failed %v; want the error and nothing marked", err, del.deleted, del.failed)
	}
}

func TestPace_StopsOnCancel(t *testing.T) {
	n := newTestNotifier(&fakeQueue{}, &fakeSender{})
	n.sendEvery = time.Hour
	n.lastCall = time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := n.pace(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("pace = %v, want context.Canceled", err)
	}
}

func TestMoneyAndMarketURL(t *testing.T) {
	for v, want := range map[float64]string{950: "$950", 48_000: "$48k", 1_200_000: "$1.2M"} {
		if got := money(v); got != want {
			t.Errorf("money(%v) = %q, want %q", v, got, want)
		}
	}
	for _, tc := range []struct{ event, slug, want string }{
		{"ev", "mk", "https://polymarket.com/event/ev/mk"},
		{"ev", "", "https://polymarket.com/event/ev"},
		{"", "mk", "https://polymarket.com/market/mk"},
	} {
		mk := db.AwesomeMarket{Market: polymarket.Market{EventSlug: tc.event, Slug: tc.slug}}
		if got := marketURL(mk); got != tc.want {
			t.Errorf("marketURL(%q, %q) = %q, want %q", tc.event, tc.slug, got, tc.want)
		}
	}
}
