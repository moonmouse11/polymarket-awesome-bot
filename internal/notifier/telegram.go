package notifier

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// Telegram posts messages to one channel. The bot must be an admin of the
// channel with the right to post messages.
type Telegram struct {
	bot    *bot.Bot
	chatID string
}

// TelegramOption configures NewTelegram (tests point it at a fake server).
type TelegramOption = bot.Option

// NewTelegram creates the client without network calls; Check verifies
// the token and the channel. channel is "@name", "name" or a numeric id
// ("-100...").
func NewTelegram(token, channel string, opts ...TelegramOption) (*Telegram, error) {
	channel = strings.TrimSpace(channel)
	if channel == "" {
		return nil, errors.New("TELEGRAM_CHANNEL_ID is empty")
	}
	if !strings.HasPrefix(channel, "@") && !strings.HasPrefix(channel, "-") {
		channel = "@" + channel // public channel username without @
	}

	// getMe is called by Check: a Telegram outage must not fail New.
	opts = append([]bot.Option{bot.WithSkipGetMe()}, opts...)
	b, err := bot.New(token, opts...)
	if err != nil {
		return nil, fmt.Errorf("telegram bot: %w", err)
	}
	return &Telegram{bot: b, chatID: channel}, nil
}

// Check verifies the token and that the channel exists. Use IsConfigError
// to tell a wrong setting from Telegram being unreachable.
func (t *Telegram) Check(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := t.bot.GetMe(ctx); err != nil {
		return fmt.Errorf("telegram token: %w", err)
	}
	if _, err := t.bot.GetChat(ctx, &bot.GetChatParams{ChatID: t.chatID}); err != nil {
		return fmt.Errorf("telegram channel %s: %w", t.chatID, err)
	}
	return nil
}

// IsConfigError reports that Telegram answered and rejected the token or the
// channel: retrying will not help, the setting must be fixed. Network errors
// and Telegram outages are not config errors.
func IsConfigError(err error) bool {
	return errors.Is(err, bot.ErrorUnauthorized) ||
		errors.Is(err, bot.ErrorBadRequest) ||
		errors.Is(err, bot.ErrorNotFound) ||
		errors.Is(err, bot.ErrorForbidden)
}

// Send posts an HTML message to the channel and returns its id, so the
// message can be edited later. Link previews are off: every message has a
// link, and a preview card under each one buries the text.
func (t *Telegram) Send(ctx context.Context, html string) (int, error) {
	noPreview := true
	msg, err := t.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:             t.chatID,
		Text:               html,
		ParseMode:          models.ParseModeHTML,
		LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: &noPreview},
	})
	if err != nil {
		return 0, err
	}
	return msg.ID, nil
}

// retryAfter returns how long Telegram asked to wait (429), or 0.
func retryAfter(err error) time.Duration {
	var tooMany *bot.TooManyRequestsError
	if errors.As(err, &tooMany) {
		return time.Duration(tooMany.RetryAfter) * time.Second
	}
	return 0
}

// isBadMessage reports that Telegram rejected the message itself (400): a
// retry would fail the same way.
func isBadMessage(err error) bool {
	return errors.Is(err, bot.ErrorBadRequest)
}
