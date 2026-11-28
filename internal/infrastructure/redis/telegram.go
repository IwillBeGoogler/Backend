package redis

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"time"
)

// TelegramTokens are single-use bot deep-link tokens. A Telegram start
// parameter allows 64 characters of base64url; these are 43.
type TelegramTokens struct{ ResetTokens }

func (t TelegramTokens) Issue(ctx context.Context, userID int, ttl time.Duration) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	return token, t.RDB.Set(ctx, t.key(token), userID, ttl).Err()
}
