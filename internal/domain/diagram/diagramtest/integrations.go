package diagramtest

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
)

// Personal access tokens (diagram.TokenRepository), webhooks
// (diagram.WebhookRepository) and the MCP audit trail
// (diagram.MCPAuditRepository).

func (m *Memory) CreateToken(_ context.Context, t diagram.AccessToken) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tokens[t.ID]; ok {
		return domain.ErrConflict
	}
	m.tokens[t.ID] = t
	return nil
}

func (m *Memory) Tokens(_ context.Context, userID int) ([]diagram.AccessToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []diagram.AccessToken
	for _, t := range m.tokens {
		if t.UserID == userID && t.RevokedAt == nil {
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b diagram.AccessToken) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (m *Memory) TokenByHash(_ context.Context, hash string) (diagram.AccessToken, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tokens {
		if t.Hash == hash {
			return t, nil
		}
	}
	return diagram.AccessToken{}, domain.ErrNotFound
}

func (m *Memory) RevokeToken(_ context.Context, userID int, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tokens[id]
	if !ok || t.UserID != userID || t.RevokedAt != nil {
		return domain.ErrNotFound
	}
	t.RevokedAt = &at
	m.tokens[id] = t
	return nil
}

func (m *Memory) TouchToken(_ context.Context, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.tokens[id]; ok && (t.LastUsedAt == nil || at.Sub(*t.LastUsedAt) > time.Minute) {
		t.LastUsedAt = &at
		m.tokens[id] = t
	}
	return nil
}

// Webhooks

func (m *Memory) CreateWebhook(_ context.Context, w diagram.Webhook) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hooks[w.ID] = w
	return nil
}

func (m *Memory) hooksWhere(keep func(diagram.Webhook) bool) []diagram.Webhook {
	var out []diagram.Webhook
	for _, w := range m.hooks {
		if keep(w) {
			out = append(out, w)
		}
	}
	slices.SortFunc(out, func(a, b diagram.Webhook) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

func (m *Memory) Webhooks(_ context.Context, projectID string) ([]diagram.Webhook, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hooksWhere(func(w diagram.Webhook) bool { return w.ProjectID == projectID }), nil
}

func (m *Memory) Webhook(_ context.Context, projectID, id string) (diagram.Webhook, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if w, ok := m.hooks[id]; ok && w.ProjectID == projectID {
		return w, nil
	}
	return diagram.Webhook{}, domain.ErrNotFound
}

func (m *Memory) WebhookByID(_ context.Context, id string) (diagram.Webhook, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if w, ok := m.hooks[id]; ok {
		return w, nil
	}
	return diagram.Webhook{}, domain.ErrNotFound
}

func (m *Memory) UpdateWebhook(_ context.Context, w diagram.Webhook) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.hooks[w.ID]
	if !ok {
		return domain.ErrNotFound
	}
	cur.URL, cur.Events, cur.Active = w.URL, w.Events, w.Active
	m.hooks[w.ID] = cur
	return nil
}

func (m *Memory) DeleteWebhook(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.hooks[id]; !ok {
		return domain.ErrNotFound
	}
	delete(m.hooks, id)
	for did, d := range m.deliveries {
		if d.WebhookID == id {
			delete(m.deliveries, did)
		}
	}
	return nil
}

func (m *Memory) ActiveWebhooks(_ context.Context, projectID string) ([]diagram.Webhook, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.hooksWhere(func(w diagram.Webhook) bool { return w.ProjectID == projectID && w.Active })
	slices.Reverse(out) // oldest first, as in Postgres
	return out, nil
}

func (m *Memory) CreateDelivery(_ context.Context, d diagram.Delivery) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deliveries[d.ID] = d
	return nil
}

func (m *Memory) Delivery(_ context.Context, id string) (diagram.Delivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if d, ok := m.deliveries[id]; ok {
		return d, nil
	}
	return diagram.Delivery{}, domain.ErrNotFound
}

func (m *Memory) UpdateDelivery(_ context.Context, d diagram.Delivery) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.deliveries[d.ID]
	if !ok {
		return domain.ErrNotFound
	}
	cur.Attempt, cur.Status, cur.ResponseCode, cur.Error, cur.NextAttemptAt = d.Attempt, d.Status, d.ResponseCode, d.Error, d.NextAttemptAt
	m.deliveries[d.ID] = cur
	return nil
}

func (m *Memory) Deliveries(_ context.Context, webhookID string, limit int) ([]diagram.Delivery, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []diagram.Delivery
	for _, d := range m.deliveries {
		if d.WebhookID == webhookID {
			out = append(out, d)
		}
	}
	slices.SortFunc(out, func(a, b diagram.Delivery) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(b.ID, a.ID)
	})
	return out[:min(limit, len(out))], nil
}

// MCP audit

func (m *Memory) AddAudit(_ context.Context, a diagram.MCPAudit) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.audits = append(m.audits, a)
	return nil
}

func (m *Memory) Audit(_ context.Context, userID, limit int) ([]diagram.MCPAudit, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []diagram.MCPAudit
	for i := len(m.audits) - 1; i >= 0; i-- {
		if m.audits[i].UserID == userID {
			out = append(out, m.audits[i])
		}
	}
	return out[:min(limit, len(out))], nil
}

var (
	_ diagram.TokenRepository    = (*Memory)(nil)
	_ diagram.WebhookRepository  = (*Memory)(nil)
	_ diagram.MCPAuditRepository = (*Memory)(nil)
)

// ReplaceToken overwrites a stored token (a test ages one this way).
func (m *Memory) ReplaceToken(t diagram.AccessToken) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[t.ID] = t
}

// ReplaceURL points a webhook at another URL without any check (a test
// uses it to make a stored endpoint turn bad).
func (m *Memory) ReplaceURL(id, url string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.hooks[id]
	w.URL = url
	m.hooks[id] = w
}
