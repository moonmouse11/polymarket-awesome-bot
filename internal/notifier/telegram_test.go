package notifier

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot"
)

// replyServer answers each Bot API method with a fixed JSON body and
// records the calls as "method chat_id".
func replyServer(t *testing.T, replies map[string]string) (*Telegram, *[]string) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(1 << 20)
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		calls = append(calls, method+" "+r.FormValue("chat_id"))
		body, ok := replies[method]
		if !ok {
			body = `{"ok":true,"result":[]}`
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	tg, err := NewTelegram("1:token", "-1001", bot.WithServerURL(srv.URL))
	if err != nil {
		t.Fatalf("NewTelegram: %v", err)
	}
	return tg, &calls
}

const okMessage = `{"ok":true,"result":{"message_id":7,"date":0,"chat":{"id":-1001,"type":"channel"}}}`

func TestNewTelegram_EmptyChannel(t *testing.T) {
	if _, err := NewTelegram("1:token", "  "); err == nil {
		t.Fatal("want an error for an empty channel")
	}
}

func TestTelegram_Edit(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		reply   string
		wantErr bool
	}{
		"edited":       {okMessage, false},
		"not modified": {`{"ok":false,"error_code":400,"description":"Bad Request: message is not modified"}`, false},
		"refused":      {`{"ok":false,"error_code":400,"description":"Bad Request: message to edit not found"}`, true},
	} {
		tg, calls := replyServer(t, map[string]string{"editMessageText": tc.reply})
		err := tg.Edit(ctx, 7, "<b>new</b>")
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: Edit = %v, want error %t", name, err, tc.wantErr)
		}
		if tc.wantErr && !isBadMessage(err) {
			t.Errorf("%s: Edit = %v, want a 400", name, err)
		}
		// A numeric channel id is used as is, without "@".
		if len(*calls) != 1 || (*calls)[0] != "editMessageText -1001" {
			t.Errorf("%s: calls = %q", name, *calls)
		}
	}
}

func TestTelegram_SendToAndPing(t *testing.T) {
	tg, calls := replyServer(t, map[string]string{
		"sendMessage": okMessage,
		"getMe":       `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"b"}}`,
	})
	ctx := context.Background()
	if err := tg.SendTo(ctx, 42, "hi"); err != nil {
		t.Errorf("SendTo: %v", err)
	}
	if err := tg.Ping(ctx); err != nil {
		t.Errorf("Ping: %v", err)
	}
	if len(*calls) != 2 || (*calls)[0] != "sendMessage 42" || (*calls)[1] != "getMe " {
		t.Errorf("calls = %q", *calls)
	}
}

func TestTelegram_DeleteReturnsFalse(t *testing.T) {
	tg, _ := replyServer(t, map[string]string{
		"deleteMessages": `{"ok":true,"result":false}`,
		"deleteMessage":  `{"ok":true,"result":false}`,
	})
	ctx := context.Background()
	if err := tg.Delete(ctx, []int{1}); !isBadMessage(err) {
		t.Errorf("Delete = %v, want a 400", err)
	}
	if err := tg.DeleteOne(ctx, 1); !isBadMessage(err) {
		t.Errorf("DeleteOne = %v, want a 400", err)
	}
}

func TestTelegram_SendError(t *testing.T) {
	tg, _ := replyServer(t, map[string]string{
		"sendMessage": `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 5","parameters":{"retry_after":5}}`,
	})
	_, err := tg.Send(context.Background(), "hi")
	if err == nil || retryAfter(err) != 5*time.Second {
		t.Errorf("Send = %v, retryAfter = %v; want an error and 5s", err, retryAfter(err))
	}
	if retryAfter(context.Canceled) != 0 {
		t.Error("retryAfter of a non-429 error must be 0")
	}
}

func TestTelegram_ListenStopsWithContext(t *testing.T) {
	tg, _ := replyServer(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- tg.Listen(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Listen = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Listen did not stop after cancel")
	}
}
