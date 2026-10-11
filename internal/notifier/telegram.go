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

// Edit replaces the text of a posted message. Editing does not notify
// subscribers. An unchanged text is not an error.
func (t *Telegram) Edit(ctx context.Context, messageID int, html string) error {
	noPreview := true
	_, err := t.bot.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:             t.chatID,
		MessageID:          messageID,
		Text:               html,
		ParseMode:          models.ParseModeHTML,
		LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: &noPreview},
	})
	if err != nil && strings.Contains(err.Error(), "message is not modified") {
		return nil
	}
	return err
}

// Delete deletes channel messages, up to 100 per call. Telegram skips
// messages that no longer exist; it reports a refusal (e.g. a message older
// than 48 h) as a 400 for the whole call.
func (t *Telegram) Delete(ctx context.Context, messageIDs []int) error {
	ok, err := t.bot.DeleteMessages(ctx, &bot.DeleteMessagesParams{ChatID: t.chatID, MessageIDs: messageIDs})
	if err == nil && !ok {
		return fmt.Errorf("%w: deleteMessages returned false", bot.ErrorBadRequest)
	}
	return err
}

// DeleteOne deletes one channel message; one already gone counts as deleted.
func (t *Telegram) DeleteOne(ctx context.Context, messageID int) error {
	ok, err := t.bot.DeleteMessage(ctx, &bot.DeleteMessageParams{ChatID: t.chatID, MessageID: messageID})
	if err != nil && strings.Contains(err.Error(), "message to delete not found") {
		return nil
	}
	if err == nil && !ok {
		return fmt.Errorf("%w: deleteMessage returned false", bot.ErrorBadRequest)
	}
	return err
}

// SendTo posts an HTML message to a private chat (alerts and command replies).
func (t *Telegram) SendTo(ctx context.Context, chatID int64, html string) error {
	noPreview := true
	_, err := t.bot.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:             chatID,
		Text:               html,
		ParseMode:          models.ParseModeHTML,
		LinkPreviewOptions: &models.LinkPreviewOptions{IsDisabled: &noPreview},
	})
	return err
}

// Ping checks that Telegram answers (getMe).
func (t *Telegram) Ping(ctx context.Context) error {
	_, err := t.bot.GetMe(ctx)
	return err
}

// Listen receives messages to the bot (long polling) until ctx is
// cancelled. They go to the handler given to NewTelegram via
// bot.WithDefaultHandler.
func (t *Telegram) Listen(ctx context.Context) error {
	t.bot.Start(ctx)
	return nil
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
