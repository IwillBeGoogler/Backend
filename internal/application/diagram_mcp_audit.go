package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
)

// The audit trail of MCP tool calls: who called which tool on what, how it
// ended. Arguments are summarized: file contents and secrets are never
// logged.

// secretKeys are argument names whose values are replaced entirely.
var secretKeys = map[string]bool{"dsl": true, "data": true, "password": true, "token": true, "connectionString": true, "secret": true}

// SummarizeArgs is a short, safe JSON of tool arguments: long strings
// become their length, long lists their count, content-like fields are
// redacted, and the whole is cut at 1000 characters.
func SummarizeArgs(args any) string {
	var walk func(v any, depth int) any
	walk = func(v any, depth int) any {
		switch x := v.(type) {
		case string:
			if len([]rune(x)) > 80 {
				return fmt.Sprintf("<%d chars>", len([]rune(x)))
			}
			return x
		case []any:
			if depth > 1 {
				return fmt.Sprintf("<%d items>", len(x))
			}
			out := make([]any, 0, 6)
			for i, e := range x {
				if i == 5 {
					out = append(out, fmt.Sprintf("+%d more", len(x)-5))
					break
				}
				out = append(out, walk(e, depth+1))
			}
			return out
		case map[string]any:
			if depth > 2 {
				return "<object>"
			}
			out := make(map[string]any, len(x))
			for k, e := range x {
				if secretKeys[k] {
					out[k] = "<redacted>"
				} else {
					out[k] = walk(e, depth+1)
				}
			}
			return out
		}
		return v
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // "<redacted>" stays readable
	if err := enc.Encode(walk(args, 0)); err != nil {
		return ""
	}
	b := bytes.TrimSpace(buf.Bytes())
	return truncate(string(b), 1000) // on a rune boundary: Postgres rejects invalid UTF-8
}

// RecordMCP stores an audit entry. It never fails the call it describes.
func (d *Diagrams) RecordMCP(ctx context.Context, a diagram.MCPAudit) {
	if d.MCPAudit == nil {
		return
	}
	a.ID, a.CreatedAt = diagram.NewID(diagram.PrefixAudit), d.clock()
	if err := d.MCPAudit.AddAudit(context.WithoutCancel(ctx), a); err != nil {
		zap.L().Warn("mcp audit write failed", zap.Error(err))
	}
}

// MCPAuditLog lists the caller's own MCP calls, newest first.
func (d *Diagrams) MCPAuditLog(ctx context.Context, actor, limit int) ([]diagram.MCPAudit, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.MCPAuditLog")
	defer span.End()
	if d.MCPAudit == nil {
		return nil, errors.New("mcp audit repository not configured")
	}
	return d.MCPAudit.Audit(ctx, actor, min(max(limit, 1), 500))
}
