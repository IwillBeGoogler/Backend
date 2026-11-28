package application

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/challenge"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/social"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/telegram"
)

// How the bot receives updates (TELEGRAM_MAIN_MODE).
const (
	TelegramPoll    = "poll"
	TelegramWebhook = "webhook"
)

// TelegramWebhookPath is where the API receives updates in webhook mode.
const TelegramWebhookPath = "/api/telegram/webhook"

const (
	telegramLinkTTL  = 10 * time.Minute
	telegramPollWait = 50 * time.Second
	telegramRetry    = 5 * time.Second
)

// TelegramMessage is a queued challenge notification for one linked user.
type TelegramMessage struct {
	NotificationID int64                   `json:"notification_id"`
	UserID         int                     `json:"user_id"`
	Type           social.NotificationType `json:"type"`
	ChallengeID    int                     `json:"challenge_id"`
	// Actor is the challenge owner's username, empty on the reader's own.
	Actor string `json:"actor,omitempty"`
	Text  string `json:"text"`
}

// TelegramQueue schedules messages, at most one per notification.
type TelegramQueue interface {
	SendTelegram(ctx context.Context, m TelegramMessage) error
}

// NotificationForwarder passes stored notifications on to another channel.
type NotificationForwarder interface {
	Forward(ctx context.Context, sent []social.Notification)
}

// Telegram links accounts to the bot and sends challenge notifications. It is
// nil when the bot is not configured.
type Telegram struct {
	Links      telegram.Repository
	Tokens     ResetTokens // single-use link tokens
	Bot        telegram.Bot
	Challenges challenge.Repository
	Queue      TelegramQueue
	// ActiveUser returns the username of an account that is neither deleted
	// nor banned; ok is false for any other account.
	ActiveUser func(ctx context.Context, id int) (username string, ok bool, err error)

	BotUsername   string
	SiteURL       string
	SiteName      string
	Mode          string // TelegramPoll or TelegramWebhook
	WebhookSecret string
}

// TelegramStatus is the caller's link to the bot.
type TelegramStatus struct {
	Enabled  bool       `json:"enabled"`
	Linked   bool       `json:"linked"`
	LinkedAt *time.Time `json:"linked_at"`
}

// LinkURL returns a bot deep link that connects the chat opening it to userID,
// once, within ten minutes. The token says nothing about the user.
func (t *Telegram) LinkURL(ctx context.Context, userID int) (string, error) {
	token, err := t.Tokens.Issue(ctx, userID, telegramLinkTTL)
	if err != nil {
		return "", err
	}
	return "https://t.me/" + url.PathEscape(t.BotUsername) + "?start=" + url.QueryEscape(token), nil
}

func (t *Telegram) Status(ctx context.Context, userID int) (TelegramStatus, error) {
	l, err := t.Links.Get(ctx, userID)
	if errors.Is(err, domain.ErrNotFound) {
		return TelegramStatus{Enabled: true}, nil
	}
	if err != nil {
		return TelegramStatus{}, err
	}
	return TelegramStatus{Enabled: true, Linked: true, LinkedAt: &l.LinkedAt}, nil
}

func (t *Telegram) Unlink(ctx context.Context, userID int) error { return t.Links.Unlink(ctx, userID) }

func (t *Telegram) site() string { return strings.TrimRight(t.SiteURL, "/") }

// HandleUpdate answers a message to the bot. Only private chats are served.
func (t *Telegram) HandleUpdate(ctx context.Context, u telegram.Update) error {
	if u.ChatID == 0 || u.ChatType != telegram.ChatPrivate {
		return nil
	}
	reply := func(key, username string) error {
		err := t.Bot.SendMessage(ctx, u.ChatID, telegram.Text(u.LanguageCode, key,
			"user", username, "site", t.SiteName, "url", t.site()))
		var te *telegram.Error
		if errors.As(err, &te) && te.Blocked() {
			return t.Links.UnlinkChat(ctx, u.ChatID)
		}
		return err
	}
	fields := strings.Fields(u.Text)
	cmd := ""
	if len(fields) > 0 {
		cmd, _, _ = strings.Cut(fields[0], "@")
	}
	switch {
	case cmd == "/stop":
		if err := t.Links.UnlinkChat(ctx, u.ChatID); err != nil {
			return err
		}
		return reply("stopped", "")
	case cmd == "/start" && len(fields) == 2:
		id, err := t.Tokens.Consume(ctx, fields[1])
		if err != nil { // unknown, expired or used
			return reply("invalid", "")
		}
		name, ok, err := t.ActiveUser(ctx, id)
		if err != nil {
			return err
		}
		if !ok {
			return reply("invalid", "")
		}
		if err := t.Links.Link(ctx, telegram.Link{UserID: id, ChatID: u.ChatID, LanguageCode: u.LanguageCode}); err != nil {
			return err
		}
		return reply("linked", name)
	}
	return reply("help", "")
}

func challengeNotification(n social.NotificationType) bool {
	switch n {
	case social.NotifyChallengeDay, social.NotifyChallengeMilestone, social.NotifyChallengePraise, social.NotifyChallengeNudge:
		return true
	}
	return false
}

// Forward queues a message for every delivered challenge notification whose
// recipient has linked a chat. Errors are only logged: the notification is
// already stored.
func (t *Telegram) Forward(ctx context.Context, sent []social.Notification) {
	var ids []int
	for _, s := range sent {
		if challengeNotification(s.Type) {
			ids = append(ids, s.UserID)
		}
	}
	if len(ids) == 0 {
		return
	}
	linked, err := t.Links.LinkedAmong(ctx, ids)
	if err != nil {
		zap.L().Warn("telegram: linked users", zap.Any("ctx", ctx), zap.Error(err))
		return
	}
	for _, s := range sent {
		if !challengeNotification(s.Type) || !slices.Contains(linked, s.UserID) {
			continue
		}
		m := TelegramMessage{NotificationID: s.ID, UserID: s.UserID, Type: s.Type, ChallengeID: s.ChallengeID, Actor: s.Actor, Text: s.Text}
		if err := t.Queue.SendTelegram(ctx, m); err != nil {
			zap.L().Warn("telegram: queue", zap.Any("ctx", ctx), zap.Int("user", s.UserID), zap.Error(err))
		}
	}
}

// Deliver sends a queued message (job handler). Link, account and challenge are
// checked again, as they may have changed since it was queued. A blocked bot
// removes the link; other errors are returned for the queue to classify.
func (t *Telegram) Deliver(ctx context.Context, m TelegramMessage) error {
	l, err := t.Links.Get(ctx, m.UserID)
	if err != nil {
		return ignoreNotFound(err)
	}
	if _, ok, err := t.ActiveUser(ctx, m.UserID); err != nil || !ok {
		return err
	}
	c, err := t.Challenges.Get(ctx, m.ChallengeID)
	if err != nil {
		return ignoreNotFound(err)
	}
	if c.Visibility != challenge.Public && c.UserID != m.UserID {
		return nil
	}
	text := challengeText(l.LanguageCode, m)
	err = t.Bot.SendMessage(ctx, l.ChatID, text+"\n\n"+c.Title+"\n"+t.site()+"/challenges/"+itoa(c.ID))
	var te *telegram.Error
	if errors.As(err, &te) && te.Blocked() {
		return t.Links.UnlinkChat(ctx, l.ChatID)
	}
	return err
}

// challengeText is the sentence the web client shows for the notification.
func challengeText(lang string, m TelegramMessage) string {
	key := m.Text // praise and nudge: "<kind>.<variant>"
	switch {
	case m.Type == social.NotifyChallengeDay:
		key = "day"
	case m.Type == social.NotifyChallengeMilestone && m.Actor != "":
		key = "milestone"
	case m.Type == social.NotifyChallengeMilestone:
		key = "milestoneOwn"
	}
	args := []string{"actor", m.Actor, "n", m.Text}
	if s := telegram.Text(lang, key, args...); s != "" {
		return s
	}
	return telegram.Text(lang, "other")
}

// Receive runs until ctx ends: in webhook mode it registers the webhook, in
// poll mode it reads updates (one instance only: Telegram refuses a second
// poller, and polling while a webhook is set).
func (t *Telegram) Receive(ctx context.Context) {
	pause := func() bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(telegramRetry):
			return true
		}
	}
	switch t.Mode {
	case TelegramWebhook:
		for {
			err := t.Bot.SetWebhook(ctx, t.site()+TelegramWebhookPath, t.WebhookSecret)
			if err == nil {
				zap.L().Info("telegram: webhook registered")
				return
			}
			zap.L().Warn("telegram: set webhook", zap.Error(err))
			if !pause() {
				return
			}
		}
	case TelegramPoll:
		var offset int64
		for ctx.Err() == nil {
			updates, err := t.Bot.Updates(ctx, offset, telegramPollWait)
			if err != nil {
				if ctx.Err() == nil {
					zap.L().Warn("telegram: get updates", zap.Error(err))
					pause()
				}
				continue
			}
			for _, u := range updates {
				offset = max(offset, u.ID+1)
				if err := t.HandleUpdate(ctx, u); err != nil {
					zap.L().Warn("telegram: update", zap.Error(err))
				}
			}
		}
	}
}
