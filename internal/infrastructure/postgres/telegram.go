package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/telegram"
)

// TelegramRepo implements telegram.Repository.
type TelegramRepo struct{ db *pgxpool.Pool }

func NewTelegramRepo(db *pgxpool.Pool) *TelegramRepo { return &TelegramRepo{db} }

func (r *TelegramRepo) Link(ctx context.Context, l telegram.Link) error {
	return pgx.BeginFunc(ctx, r.db, func(tx pgx.Tx) error {
		// the chat leaves whoever had it; the user's old chat is replaced
		if _, err := tx.Exec(ctx, `DELETE FROM telegram_links WHERE chat_id = $1 AND user_id <> $2`, l.ChatID, l.UserID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO telegram_links (user_id, chat_id, language_code) VALUES ($1, $2, $3)
			ON CONFLICT (user_id) DO UPDATE SET chat_id = EXCLUDED.chat_id, language_code = EXCLUDED.language_code, linked_at = now()`,
			l.UserID, l.ChatID, l.LanguageCode)
		return err
	})
}

func (r *TelegramRepo) Get(ctx context.Context, userID int) (telegram.Link, error) {
	l := telegram.Link{UserID: userID}
	err := r.db.QueryRow(ctx, `SELECT chat_id, language_code, linked_at FROM telegram_links WHERE user_id = $1`, userID).
		Scan(&l.ChatID, &l.LanguageCode, &l.LinkedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return l, domain.ErrNotFound
	}
	return l, err
}

func (r *TelegramRepo) Unlink(ctx context.Context, userID int) error {
	_, err := r.db.Exec(ctx, `DELETE FROM telegram_links WHERE user_id = $1`, userID)
	return err
}

func (r *TelegramRepo) UnlinkChat(ctx context.Context, chatID int64) error {
	_, err := r.db.Exec(ctx, `DELETE FROM telegram_links WHERE chat_id = $1`, chatID)
	return err
}

func (r *TelegramRepo) LinkedAmong(ctx context.Context, userIDs []int) ([]int, error) {
	rows, err := r.db.Query(ctx, `SELECT user_id FROM telegram_links WHERE user_id = ANY($1::int[])`, userIDs)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int])
}
