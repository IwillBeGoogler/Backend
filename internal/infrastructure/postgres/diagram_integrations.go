package postgres

import (
	"context"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
	"github.com/jackc/pgx/v5"
)

// Personal access tokens (diagram.TokenRepository), webhooks with their
// deliveries (diagram.WebhookRepository) and the MCP audit trail
// (diagram.MCPAuditRepository).

const tokenCols = `id, user_id, name, token_hash, prefix, scopes, project_ids, expires_at, last_used_at, revoked_at, created_at`

func scanToken(row pgx.Row) (diagram.AccessToken, error) {
	var t diagram.AccessToken
	err := row.Scan(&t.ID, &t.UserID, &t.Name, &t.Hash, &t.Prefix, &t.Scopes, &t.ProjectIDs, &t.ExpiresAt, &t.LastUsedAt,
		&t.RevokedAt, &t.CreatedAt)
	return t, notFound(err)
}

func (r *DiagramRepo) CreateToken(ctx context.Context, t diagram.AccessToken) error {
	_, err := r.db.Exec(ctx, `INSERT INTO dg_access_tokens (`+tokenCols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		t.ID, t.UserID, t.Name, t.Hash, t.Prefix, t.Scopes, t.ProjectIDs, t.ExpiresAt, t.LastUsedAt, t.RevokedAt, t.CreatedAt)
	return err
}

func (r *DiagramRepo) Tokens(ctx context.Context, userID int) ([]diagram.AccessToken, error) {
	rows, err := r.db.Query(ctx, `SELECT `+tokenCols+` FROM dg_access_tokens WHERE user_id = $1 AND revoked_at IS NULL
		ORDER BY created_at DESC, id`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (diagram.AccessToken, error) { return scanToken(row) })
}

func (r *DiagramRepo) TokenByHash(ctx context.Context, hash string) (diagram.AccessToken, error) {
	return scanToken(r.db.QueryRow(ctx, `SELECT `+tokenCols+` FROM dg_access_tokens WHERE token_hash = $1`, hash))
}

func (r *DiagramRepo) RevokeToken(ctx context.Context, userID int, id string, at time.Time) error {
	return execOne(ctx, r.db, `UPDATE dg_access_tokens SET revoked_at = $3 WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`,
		id, userID, at)
}

func (r *DiagramRepo) TouchToken(ctx context.Context, id string, at time.Time) error {
	_, err := r.db.Exec(ctx, `UPDATE dg_access_tokens SET last_used_at = $2::timestamptz
		WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < $2::timestamptz - interval '1 minute')`, id, at)
	return err
}

// Webhooks

const hookCols = `id, project_id, url, secret, events, active, COALESCE(created_by, 0), created_at`

func scanHook(row pgx.Row) (diagram.Webhook, error) {
	var w diagram.Webhook
	err := row.Scan(&w.ID, &w.ProjectID, &w.URL, &w.Secret, &w.Events, &w.Active, &w.CreatedBy, &w.CreatedAt)
	return w, notFound(err)
}

func (r *DiagramRepo) CreateWebhook(ctx context.Context, w diagram.Webhook) error {
	var by *int
	if w.CreatedBy != 0 {
		by = &w.CreatedBy
	}
	_, err := r.db.Exec(ctx, `INSERT INTO dg_webhooks (id, project_id, url, secret, events, active, created_by, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, w.ID, w.ProjectID, w.URL, w.Secret, w.Events, w.Active, by, w.CreatedAt)
	return err
}

func (r *DiagramRepo) Webhooks(ctx context.Context, projectID string) ([]diagram.Webhook, error) {
	rows, err := r.db.Query(ctx, `SELECT `+hookCols+` FROM dg_webhooks WHERE project_id = $1 ORDER BY created_at DESC, id`, projectID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (diagram.Webhook, error) { return scanHook(row) })
}

func (r *DiagramRepo) Webhook(ctx context.Context, projectID, id string) (diagram.Webhook, error) {
	return scanHook(r.db.QueryRow(ctx, `SELECT `+hookCols+` FROM dg_webhooks WHERE id = $1 AND project_id = $2`, id, projectID))
}

func (r *DiagramRepo) WebhookByID(ctx context.Context, id string) (diagram.Webhook, error) {
	return scanHook(r.db.QueryRow(ctx, `SELECT `+hookCols+` FROM dg_webhooks WHERE id = $1`, id))
}

func (r *DiagramRepo) UpdateWebhook(ctx context.Context, w diagram.Webhook) error {
	return execOne(ctx, r.db, `UPDATE dg_webhooks SET url = $2, events = $3, active = $4 WHERE id = $1`, w.ID, w.URL, w.Events, w.Active)
}

func (r *DiagramRepo) DeleteWebhook(ctx context.Context, id string) error {
	return execOne(ctx, r.db, `DELETE FROM dg_webhooks WHERE id = $1`, id) // deliveries cascade
}

func (r *DiagramRepo) ActiveWebhooks(ctx context.Context, projectID string) ([]diagram.Webhook, error) {
	rows, err := r.db.Query(ctx, `SELECT `+hookCols+` FROM dg_webhooks WHERE project_id = $1 AND active ORDER BY created_at, id`, projectID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (diagram.Webhook, error) { return scanHook(row) })
}

const deliveryCols = `id, webhook_id, event, payload, attempt, status, response_code, COALESCE(error, ''), next_attempt_at, created_at`

func scanDelivery(row pgx.Row) (diagram.Delivery, error) {
	var d diagram.Delivery
	err := row.Scan(&d.ID, &d.WebhookID, &d.Event, &d.Payload, &d.Attempt, &d.Status, &d.ResponseCode, &d.Error, &d.NextAttemptAt, &d.CreatedAt)
	return d, notFound(err)
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (r *DiagramRepo) CreateDelivery(ctx context.Context, d diagram.Delivery) error {
	_, err := r.db.Exec(ctx, `INSERT INTO dg_webhook_deliveries (id, webhook_id, event, payload, attempt, status, response_code, error, next_attempt_at, created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		d.ID, d.WebhookID, d.Event, d.Payload, d.Attempt, d.Status, d.ResponseCode, nullIfEmpty(d.Error), d.NextAttemptAt, d.CreatedAt)
	return err
}

func (r *DiagramRepo) Delivery(ctx context.Context, id string) (diagram.Delivery, error) {
	return scanDelivery(r.db.QueryRow(ctx, `SELECT `+deliveryCols+` FROM dg_webhook_deliveries WHERE id = $1`, id))
}

func (r *DiagramRepo) UpdateDelivery(ctx context.Context, d diagram.Delivery) error {
	return execOne(ctx, r.db, `UPDATE dg_webhook_deliveries SET attempt = $2, status = $3, response_code = $4, error = $5,
		next_attempt_at = $6 WHERE id = $1`, d.ID, d.Attempt, d.Status, d.ResponseCode, nullIfEmpty(d.Error), d.NextAttemptAt)
}

func (r *DiagramRepo) Deliveries(ctx context.Context, webhookID string, limit int) ([]diagram.Delivery, error) {
	rows, err := r.db.Query(ctx, `SELECT `+deliveryCols+` FROM dg_webhook_deliveries WHERE webhook_id = $1
		ORDER BY created_at DESC, id DESC LIMIT $2`, webhookID, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (diagram.Delivery, error) { return scanDelivery(row) })
}

// MCP audit

func (r *DiagramRepo) AddAudit(ctx context.Context, a diagram.MCPAudit) error {
	var ms *int
	if a.DurationMS > 0 {
		ms = &a.DurationMS
	}
	_, err := r.db.Exec(ctx, `INSERT INTO dg_mcp_audit (id, user_id, token_id, tool, project_id, file_id, status, detail, duration_ms, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, a.ID, a.UserID, nullIfEmpty(a.TokenID), a.Tool, nullIfEmpty(a.ProjectID),
		nullIfEmpty(a.FileID), a.Status, nullIfEmpty(a.Detail), ms, a.CreatedAt)
	return err
}

func (r *DiagramRepo) Audit(ctx context.Context, userID, limit int) ([]diagram.MCPAudit, error) {
	rows, err := r.db.Query(ctx, `SELECT id, user_id, COALESCE(token_id, ''), tool, COALESCE(project_id, ''), COALESCE(file_id, ''),
		status, COALESCE(detail, ''), COALESCE(duration_ms, 0), created_at
		FROM dg_mcp_audit WHERE user_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (diagram.MCPAudit, error) {
		var a diagram.MCPAudit
		err := row.Scan(&a.ID, &a.UserID, &a.TokenID, &a.Tool, &a.ProjectID, &a.FileID, &a.Status, &a.Detail, &a.DurationMS, &a.CreatedAt)
		return a, err
	})
}

var (
	_ diagram.TokenRepository    = (*DiagramRepo)(nil)
	_ diagram.WebhookRepository  = (*DiagramRepo)(nil)
	_ diagram.MCPAuditRepository = (*DiagramRepo)(nil)
)
