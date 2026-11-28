// Package telegram is the HTTP client of the Telegram Bot API.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/telegram"
)

// DefaultURL is the public Bot API.
const DefaultURL = "https://api.telegram.org"

const (
	sendsPerSecond = 25 // under Telegram's overall limit of about 30
	callTimeout    = 10 * time.Second
)

// Client implements telegram.Bot. The token is part of every request URL, so
// no error from net/http leaves this package unredacted.
type Client struct {
	token, base string
	http        *http.Client
	sends       *rate.Limiter
}

// New returns a client; base is "" outside tests.
func New(token, base string) *Client {
	if base == "" {
		base = DefaultURL
	}
	return &Client{token: token, base: strings.TrimRight(base, "/"), sends: rate.NewLimiter(sendsPerSecond, 1),
		// a redirect would carry the token elsewhere
		http: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

// String keeps the token out of anything that prints a client.
func (c *Client) String() string   { return "telegram.Client" }
func (c *Client) GoString() string { return c.String() }

func (c *Client) redact(s string) string {
	if c.token == "" {
		return s
	}
	s = strings.ReplaceAll(s, c.token, "<token>")
	return strings.ReplaceAll(s, url.PathEscape(c.token), "<token>")
}

// transportError drops the URL of a *url.Error but keeps its cause for
// errors.Is (context cancellation, timeouts).
type transportError struct {
	msg   string
	cause error
}

func (e transportError) Error() string { return e.msg }
func (e transportError) Is(target error) bool {
	return errors.Is(e.cause, target)
}

func (c *Client) fail(method string, err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return transportError{msg: "telegram: " + method + ": " + c.redact(err.Error()), cause: err}
}

func (c *Client) call(ctx context.Context, timeout time.Duration, method string, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	body, err := json.Marshal(in)
	if err != nil {
		return c.fail(method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/bot"+c.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return c.fail(method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req) //nolint:gosec // the host is operator configuration
	if err != nil {
		return c.fail(method, err)
	}
	defer res.Body.Close()
	var reply struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&reply); err != nil {
		if res.StatusCode >= 400 {
			return &telegram.Error{Code: res.StatusCode, Description: method + " failed"}
		}
		return c.fail(method, fmt.Errorf("bad response: %w", err))
	}
	if !reply.OK {
		code := reply.ErrorCode
		if code == 0 {
			code = res.StatusCode
		}
		return &telegram.Error{Code: code, Description: method + ": " + c.redact(reply.Description),
			RetryAfter: time.Duration(reply.Parameters.RetryAfter) * time.Second}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(reply.Result, out); err != nil {
		return c.fail(method, err)
	}
	return nil
}

// SendMessage sends plain text, waiting for the rate limit.
func (c *Client) SendMessage(ctx context.Context, chatID int64, text string) error {
	if err := c.sends.Wait(ctx); err != nil {
		return c.fail("sendMessage", err)
	}
	return c.call(ctx, callTimeout, "sendMessage", map[string]any{"chat_id": chatID, "text": text}, nil)
}

func (c *Client) SetWebhook(ctx context.Context, hook, secret string) error {
	return c.call(ctx, callTimeout, "setWebhook", map[string]any{"url": hook, "secret_token": secret, "allowed_updates": []string{"message"}}, nil)
}

func (c *Client) Updates(ctx context.Context, offset int64, wait time.Duration) ([]telegram.Update, error) {
	var raw []Update
	err := c.call(ctx, wait+callTimeout, "getUpdates", map[string]any{
		"offset": offset, "timeout": int(wait / time.Second), "allowed_updates": []string{"message"}}, &raw)
	out := make([]telegram.Update, len(raw))
	for i, u := range raw {
		out[i] = u.Domain()
	}
	return out, err
}

// Update is the wire form of an update, as polled or posted to the webhook.
type Update struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		Text string `json:"text"`
		Chat struct {
			ID   int64  `json:"id"`
			Type string `json:"type"`
		} `json:"chat"`
		From struct {
			LanguageCode string `json:"language_code"`
		} `json:"from"`
	} `json:"message"`
}

func (u Update) Domain() telegram.Update {
	out := telegram.Update{ID: u.UpdateID}
	if m := u.Message; m != nil {
		out.ChatID, out.ChatType, out.Text = m.Chat.ID, m.Chat.Type, m.Text
		out.LanguageCode = m.From.LanguageCode
		if len(out.LanguageCode) > 35 {
			out.LanguageCode = ""
		}
	}
	return out
}
