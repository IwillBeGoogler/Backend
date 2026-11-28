package application

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/challenge"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/social"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/telegram"
)

type tgLinks map[int]telegram.Link

func (f tgLinks) Link(_ context.Context, l telegram.Link) error {
	for id, o := range f {
		if o.ChatID == l.ChatID {
			delete(f, id)
		}
	}
	f[l.UserID] = l
	return nil
}
func (f tgLinks) Get(_ context.Context, id int) (telegram.Link, error) {
	l, ok := f[id]
	if !ok {
		return l, domain.ErrNotFound
	}
	return l, nil
}
func (f tgLinks) Unlink(_ context.Context, id int) error { delete(f, id); return nil }
func (f tgLinks) UnlinkChat(_ context.Context, chat int64) error {
	for id, l := range f {
		if l.ChatID == chat {
			delete(f, id)
		}
	}
	return nil
}
func (f tgLinks) LinkedAmong(_ context.Context, ids []int) ([]int, error) {
	return slices.DeleteFunc(slices.Clone(ids), func(id int) bool { _, ok := f[id]; return !ok }), nil
}

type tgTokens map[string]int

func (f tgTokens) Issue(_ context.Context, id int, _ time.Duration) (string, error) {
	f["tok"] = id
	return "tok", nil
}
func (f tgTokens) Consume(_ context.Context, tok string) (int, error) {
	id, ok := f[tok]
	if !ok {
		return 0, errors.New("none")
	}
	delete(f, tok)
	return id, nil
}

type tgBot struct {
	telegram.Bot
	sent []string
	err  error
}

func (b *tgBot) SendMessage(_ context.Context, _ int64, text string) error {
	b.sent = append(b.sent, text)
	return b.err
}

type tgChallenges struct {
	challenge.Repository
	c challenge.Challenge
}

func (f tgChallenges) Get(context.Context, int) (challenge.Challenge, error) { return f.c, nil }

type tgQueue []TelegramMessage

func (q *tgQueue) SendTelegram(_ context.Context, m TelegramMessage) error {
	*q = append(*q, m)
	return nil
}

func newTelegram() (*Telegram, tgLinks, *tgBot, *tgQueue) {
	links, bot, q := tgLinks{}, &tgBot{}, &tgQueue{}
	return &Telegram{Links: links, Tokens: tgTokens{}, Bot: bot, Queue: q,
		Challenges:  tgChallenges{c: challenge.Challenge{ID: 3, UserID: 1, Title: "Go", Visibility: challenge.Private}},
		ActiveUser:  func(_ context.Context, id int) (string, bool, error) { return "ali", id != 9, nil },
		BotUsername: "testbot", SiteURL: "https://site.test/", SiteName: "Site"}, links, bot, q
}

func TestTelegramLinking(t *testing.T) {
	ctx := context.Background()
	tg, links, bot, _ := newTelegram()
	send := func(chat int64, kind, text string) string {
		t.Helper()
		bot.sent = nil
		if err := tg.HandleUpdate(ctx, telegram.Update{ChatID: chat, ChatType: kind, Text: text}); err != nil {
			t.Fatal(err)
		}
		return strings.Join(bot.sent, "")
	}
	u, err := tg.LinkURL(ctx, 1)
	if err != nil || u != "https://t.me/testbot?start=tok" {
		t.Fatalf("url = %q, %v", u, err)
	}
	if s := send(10, "group", "/start tok"); s != "" || len(links) != 0 {
		t.Fatalf("group chat served: %q", s)
	}
	if s := send(10, "private", "/start tok"); !strings.Contains(s, "@ali") || !strings.Contains(s, "/stop") || links[1].ChatID != 10 {
		t.Fatalf("linked = %q %v", s, links)
	}
	if s := send(11, "private", "/start tok"); !strings.Contains(s, "eskirgan") || len(links) != 1 {
		t.Errorf("token reused: %q", s)
	}
	_, _ = tg.LinkURL(ctx, 2) // the same chat moves to another account
	send(10, "private", "/start tok")
	if len(links) != 1 || links[2].ChatID != 10 {
		t.Errorf("one user per chat: %v", links)
	}
	_, _ = tg.LinkURL(ctx, 9) // banned or deleted
	send(12, "private", "/start tok")
	if _, ok := links[9]; ok {
		t.Error("inactive account linked")
	}
	if s := send(10, "private", "hello"); !strings.Contains(s, "https://site.test") {
		t.Errorf("help = %q", s)
	}
	if send(10, "private", "/stop"); len(links) != 0 {
		t.Errorf("stop: %v", links)
	}
}

func TestTelegramNotifications(t *testing.T) {
	ctx := context.Background()
	tg, links, bot, q := newTelegram()
	links[1] = telegram.Link{UserID: 1, ChatID: 10}
	links[2] = telegram.Link{UserID: 2, ChatID: 20, LanguageCode: "en-US"}
	links[9] = telegram.Link{UserID: 9, ChatID: 90}
	tg.Forward(ctx, []social.Notification{
		{ID: 1, UserID: 1, Type: social.NotifyChallengeNudge, ChallengeID: 3, Text: "evening.0"},
		{ID: 2, UserID: 4, Type: social.NotifyChallengeNudge, ChallengeID: 3, Text: "evening.0"},
		{ID: 3, UserID: 1, Type: social.NotifyFollow},
	})
	if len(*q) != 1 || (*q)[0].NotificationID != 1 {
		t.Fatalf("queued = %+v", *q)
	}
	if err := tg.Deliver(ctx, (*q)[0]); err != nil || len(bot.sent) != 1 {
		t.Fatalf("deliver: %v %v", err, bot.sent)
	}
	if s := bot.sent[0]; !strings.Contains(s, "Sen uddalaysan!") || !strings.Contains(s, "Go\nhttps://site.test/challenges/3") {
		t.Errorf("text = %q", s)
	}
	bot.sent = nil
	for _, id := range []int{2, 4, 9} { // not the private challenge's owner, unlinked, inactive
		if err := tg.Deliver(ctx, TelegramMessage{UserID: id, ChallengeID: 3}); err != nil {
			t.Fatal(err)
		}
	}
	if len(bot.sent) != 0 {
		t.Errorf("sent = %v", bot.sent)
	}
	bot.err = &telegram.Error{Code: 429}
	if err := tg.Deliver(ctx, (*q)[0]); err == nil || len(links) != 3 {
		t.Errorf("429 = %v", err)
	}
	bot.err = &telegram.Error{Code: 403}
	if err := tg.Deliver(ctx, (*q)[0]); err != nil {
		t.Fatal(err)
	}
	if _, ok := links[1]; ok {
		t.Error("blocked bot keeps the link")
	}
}

func TestChallengeText(t *testing.T) {
	for _, lang := range []string{"uz", "en", "ru"} {
		for _, kind := range []string{"praise", "evening", "broken", "silent"} {
			for i := range challenge.Variants {
				m := TelegramMessage{Type: social.NotifyChallengeNudge, Text: kind + "." + itoa(i)}
				if challengeText(lang, m) == telegram.Text(lang, "other") {
					t.Errorf("%s: no text for %s", lang, m.Text)
				}
			}
		}
	}
	got := challengeText("en", TelegramMessage{Type: social.NotifyChallengeDay, Actor: "ali", Text: "4"})
	if got != "ali wrote day 4 of their challenge" {
		t.Errorf("day = %q", got)
	}
}
