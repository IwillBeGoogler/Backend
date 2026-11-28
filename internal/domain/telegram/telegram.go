// Package telegram links accounts to Telegram chats of the site's bot.
package telegram

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Link is one account's chat with the bot: one chat per user, one user per chat.
type Link struct {
	UserID       int
	ChatID       int64
	LanguageCode string
	LinkedAt     time.Time
}

// Repository stores links. Missing rows are domain.ErrNotFound.
type Repository interface {
	// Link binds chatID to l.UserID, replacing any link of either side.
	Link(ctx context.Context, l Link) error
	Get(ctx context.Context, userID int) (Link, error)
	// Unlink and UnlinkChat are no-ops when there is nothing to remove.
	Unlink(ctx context.Context, userID int) error
	UnlinkChat(ctx context.Context, chatID int64) error
	// LinkedAmong returns those of userIDs that have a chat.
	LinkedAmong(ctx context.Context, userIDs []int) ([]int, error)
}

// ChatPrivate is the type of a one-to-one chat with the bot.
const ChatPrivate = "private"

// Update is an incoming message; other kinds of update have ChatID 0.
type Update struct {
	ID           int64
	ChatID       int64
	ChatType     string
	Text         string
	LanguageCode string
}

// Bot calls the Telegram Bot API. Failed calls the API answered are *Error;
// no error ever contains the bot token.
type Bot interface {
	// SendMessage sends plain text.
	SendMessage(ctx context.Context, chatID int64, text string) error
	SetWebhook(ctx context.Context, url, secret string) error
	// Updates long-polls for messages with ID >= offset.
	Updates(ctx context.Context, offset int64, wait time.Duration) ([]Update, error)
}

// Error is a refusal by the Bot API.
type Error struct {
	Code        int
	Description string
	// RetryAfter is set with Code 429.
	RetryAfter time.Duration
}

func (e *Error) Error() string { return fmt.Sprintf("telegram: %d %s", e.Code, e.Description) }

// Blocked reports that the chat can no longer be written to (the user blocked
// the bot or deleted the account).
func (e *Error) Blocked() bool { return e.Code == 403 }

// Permanent reports that repeating the same call cannot succeed.
func (e *Error) Permanent() bool { return e.Code >= 400 && e.Code < 500 && e.Code != 429 }

// Lang maps a Telegram language_code to a supported language, "uz" by default.
func Lang(code string) string {
	base, _, _ := strings.Cut(strings.ToLower(code), "-")
	switch base {
	case "ru", "en":
		return base
	}
	return "uz"
}
