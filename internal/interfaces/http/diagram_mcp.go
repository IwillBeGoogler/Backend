package http

// The MCP server of the diagram workspace: Streamable HTTP at
// /api/diagrams/mcp, stateless (one server per request, JSON responses), on
// the official Go SDK. Authenticate with an access token ("sdd_pat_...",
// needs projects:read, then each tool checks its own scope) or a session.
// Every tool call is authorized against the token's scopes and its owner's
// project role, rate limited per token, and written to the audit log.
//
// The tools that need the TypeScript core (diagram language, icon catalog,
// layouts, lint, text exporters) are listed but answer that they are not
// available on this server.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
)

func (h *Handler) mcpRoutes(mux dgRouter) {
	if !h.MCPEnabled {
		return
	}
	endpoint := h.mcpEndpoint()
	mux.HandleFunc("POST "+DiagramMCPPath, endpoint)
	mux.HandleFunc("GET "+DiagramMCPPath, endpoint)
	mux.HandleFunc("DELETE "+DiagramMCPPath, endpoint)
	mux.HandleFunc("GET "+DiagramMCPPath+"/audit", h.dgMCPAudit)
}

// rpcError writes a JSON-RPC error body.
func rpcError(w http.ResponseWriter, status, code int, msg string, hdr ...string) {
	w.Header().Set("Content-Type", "application/json")
	for i := 0; i+1 < len(hdr); i += 2 {
		w.Header().Set(hdr[i], hdr[i+1])
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "error": map[string]any{"code": code, "message": msg}, "id": nil})
}

func (h *Handler) mcpEndpoint() http.HandlerFunc {
	sdk := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server { return h.mcpServer(r) },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	return func(w http.ResponseWriter, r *http.Request) {
		if t := tokenFrom(r.Context()); t != nil && !t.Allows(diagram.ScopeProjectsRead) {
			rpcError(w, http.StatusForbidden, -32001, `The access token needs the "projects:read" scope`)
			return
		}
		if r.Method != http.MethodPost {
			rpcError(w, http.StatusMethodNotAllowed, -32000, "Method not allowed: this MCP endpoint is stateless, use POST", "Allow", "POST")
			return
		}
		if h.viewerID(r) == 0 {
			rpcError(w, http.StatusUnauthorized, -32001, "Authentication required")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, h.docLimit())
		sdk.ServeHTTP(w, r)
	}
}

// dgMCPAudit lists the caller's MCP calls, newest first. Query: limit
// (1-500, default 100). Response: {"items": [{id, userId, tokenId, tool,
// projectId, fileId, status, detail, durationMs, createdAt}]}. status is ok,
// error, denied or rate_limited. Browser session only: a token cannot read
// its own trail.
//
// spector:tags diagrams
func (h *Handler) dgMCPAudit(w http.ResponseWriter, r *http.Request) {
	if tokenFrom(r.Context()) != nil {
		dgFail(w, r, http.StatusForbidden, "TOKEN_SCOPE", "This endpoint cannot be used with an access token", nil)
		return
	}
	uid, ok := h.dgUser(w, r)
	if !ok {
		return
	}
	limit := 100
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			limit = min(max(n, 1), 500)
		}
	}
	rows, err := h.Diagrams.MCPAuditLog(r.Context(), uid, limit)
	if err != nil {
		dgError(w, r, err)
		return
	}
	items := make([]map[string]any, len(rows))
	for i, a := range rows {
		items[i] = map[string]any{"id": a.ID, "userId": strconv.Itoa(a.UserID), "tokenId": nilIfEmpty(a.TokenID), "tool": a.Tool,
			"projectId": nilIfEmpty(a.ProjectID), "fileId": nilIfEmpty(a.FileID), "status": a.Status, "detail": nilIfEmpty(a.Detail),
			"durationMs": a.DurationMS, "createdAt": ms(a.CreatedAt)}
		if a.DurationMS == 0 {
			items[i]["durationMs"] = nil
		}
	}
	dgWrite(w, http.StatusOK, map[string]any{"items": items})
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Rate limit

// rateWindow is a sliding window per key: n calls in the last window. It
// lives in this process, which is right for one instance.
type rateWindow struct {
	n      int
	window time.Duration
	now    func() time.Time
	mu     sync.Mutex
	hits   map[string][]time.Time
}

func newRateWindow(n int, window time.Duration) *rateWindow {
	if n <= 0 {
		n = 120
	}
	return &rateWindow{n: n, window: window, now: time.Now, hits: map[string][]time.Time{}}
}

// take records a call and returns 0, or returns the seconds to wait when
// the window is full (recording nothing).
func (w *rateWindow) take(key string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	recent := w.hits[key][:0]
	for _, t := range w.hits[key] {
		if now.Sub(t) < w.window {
			recent = append(recent, t)
		}
	}
	if len(recent) >= w.n {
		w.hits[key] = recent
		return max(1, int((recent[0].Add(w.window).Sub(now)+time.Second-1)/time.Second))
	}
	w.hits[key] = append(recent, now)
	if len(w.hits) > 10_000 { // forget keys that went quiet
		for k, ts := range w.hits {
			if len(ts) == 0 || now.Sub(ts[len(ts)-1]) >= w.window {
				delete(w.hits, k)
			}
		}
	}
	return 0
}

// Server and tools

const mcpInstructions = `Read and edit system design diagrams. Start with list_projects, then list_files / read_file. Edits are live: collaborators see them immediately, attributed "<you> via MCP". Find icons with search_catalog and pass the returned id as add_nodes.icon. Use apply_layout to arrange a page, import_dsl / export (format sdd) for diagrams as code, and run_lint to check a page.`

type mcpCall struct {
	h     *Handler
	ctx   context.Context
	uid   int
	token *tokenAuth
}

// toolError is a failure the tool reports to the client (with details).
type toolError struct {
	msg     string
	details any
}

func (e *toolError) Error() string { return e.msg }

type mcpTool struct {
	name, title, description string
	scope                    string
	write, destructive       bool
	schema                   map[string]any
	run                      func(c *mcpCall, raw json.RawMessage) (any, error)
}

func (h *Handler) mcpServer(r *http.Request) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "system-design-diagram", Version: "0.1.0"}, &mcp.ServerOptions{Instructions: mcpInstructions})
	call := &mcpCall{h: h, ctx: r.Context(), uid: h.viewerID(r), token: tokenFrom(r.Context())}
	no := false
	for _, t := range mcpTools {
		srv.AddTool(&mcp.Tool{Name: t.name, Title: t.title, Description: t.description, InputSchema: t.schema,
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: !t.write, DestructiveHint: boolPtr(t.destructive), OpenWorldHint: &no}},
			func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return h.runTool(call, t, req.Params.Arguments), nil
			})
	}
	return srv
}

func boolPtr(b bool) *bool { return &b }

func mcpText(v any, isError bool) *mcp.CallToolResult {
	text, ok := v.(string)
	if !ok {
		b, _ := json.Marshal(v)
		text = string(b)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}, IsError: isError}
}

// runTool checks scope and rate, runs the tool and writes the audit entry.
func (h *Handler) runTool(c *mcpCall, t mcpTool, raw json.RawMessage) *mcp.CallToolResult {
	started := time.Now()
	var args map[string]any
	_ = json.Unmarshal(raw, &args)
	audit := diagram.MCPAudit{UserID: c.uid, Tool: t.name, ProjectID: argString(args, "projectId"), FileID: argString(args, "fileId")}
	if c.token != nil {
		audit.TokenID = c.token.ID
	}
	record := func(status string, detail any) {
		audit.Status, audit.DurationMS = status, int(time.Since(started).Milliseconds())
		audit.Detail = application.SummarizeArgs(detail)
		h.Diagrams.RecordMCP(c.ctx, audit)
	}
	if c.uid == 0 {
		return mcpText("authentication required", true)
	}
	if c.token != nil && !c.token.Allows(t.scope) {
		record(diagram.AuditDenied, args)
		return mcpText(fmt.Sprintf("the access token needs the %q scope for %s", t.scope, t.name), true)
	}
	key := "u:" + strconv.Itoa(c.uid)
	if c.token != nil {
		key = c.token.ID
	}
	if wait := h.mcpLimit.take(key); wait > 0 {
		record(diagram.AuditRateLimited, args)
		return mcpText(fmt.Sprintf("rate limit exceeded (%d calls per minute); retry in %ds", h.mcpLimit.n, wait), true)
	}
	out, err := t.run(c, raw)
	if err != nil {
		status := diagram.AuditError
		if errors.Is(err, domain.ErrForbidden) || errors.Is(err, domain.ErrNotFound) {
			status = diagram.AuditDenied
		}
		detail := map[string]any{"error": truncate(err.Error(), 200)}
		for k, v := range args {
			detail[k] = v
		}
		record(status, detail)
		var te *toolError
		if errors.As(err, &te) && te.details != nil {
			return mcpText(map[string]any{"error": te.msg, "details": te.details}, true)
		}
		return mcpText(toolMessage(err), true)
	}
	record(diagram.AuditOK, args)
	return mcpText(out, false)
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func argString(args map[string]any, k string) string {
	s, _ := args[k].(string)
	return s
}

// toolMessage is an error as the client reads it.
func toolMessage(err error) string {
	var de *diagram.Error
	if errors.As(err, &de) {
		return de.Msg
	}
	return err.Error()
}
