package queue

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hibiken/asynq"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/telegram"
)

func TestTelegramTask(t *testing.T) {
	run := func(result error) error {
		h := telegramTask(func(context.Context, application.TelegramMessage) error { return result })
		return h(context.Background(), asynq.NewTask(TypeTelegram, []byte(`{"user_id":1}`)))
	}
	if err := run(nil); err != nil {
		t.Fatal(err)
	}
	if err := run(&telegram.Error{Code: 400}); !errors.Is(err, asynq.SkipRetry) {
		t.Errorf("400 retried: %v", err)
	}
	for _, e := range []error{&telegram.Error{Code: 429}, &telegram.Error{Code: 502}, errors.New("network")} {
		if err := run(e); err == nil || errors.Is(err, asynq.SkipRetry) {
			t.Errorf("%v: %v", e, err)
		}
	}
	if d := retryDelay(0, &telegram.Error{Code: 429, RetryAfter: 30 * time.Second}, nil); d != 31*time.Second {
		t.Errorf("delay = %s", d)
	}
}
