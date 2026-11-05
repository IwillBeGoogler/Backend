package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/domain"
	"github.com/iBlog/iblog-monolith-go/internal/domain/diagram"
)

// importer moves the old server's data into the dg_* tables.
type importer struct {
	src      Source
	st       store
	blobsIn  BlobSource // nil: assets cannot be read
	blobsOut BlobSink   // nil: dry run, no uploads

	createMissing bool
	dry           bool
	// assumeUsers maps every old user to a placeholder (dry run without a
	// reachable target database).
	assumeUsers bool

	out   io.Writer
	rep   *report
	users map[string]int
	now   time.Time
	conv  *converter
}

func (im *importer) logf(format string, a ...any) {
	if im.out != nil {
		fmt.Fprintf(im.out, format+"\n", a...)
	}
}

func (im *importer) run(ctx context.Context) (*report, error) {
	im.rep = newReport()
	im.users = map[string]int{}
	if im.now.IsZero() {
		im.now = time.Now().UTC()
	}
	im.conv = &converter{src: im.src, users: im.users, now: im.now}
	if err := im.importUsers(ctx); err != nil {
		return im.rep, fmt.Errorf("users: %w", err)
	}
	if err := im.importGlobal(ctx); err != nil {
		return im.rep, fmt.Errorf("global data: %w", err)
	}
	if err := im.importProjects(ctx); err != nil {
		return im.rep, fmt.Errorf("projects: %w", err)
	}
	return im.rep, nil
}

var nonName = regexp.MustCompile(`[^A-Za-z0-9_]+`)

func sanitizeName(s string) string { return strings.Trim(nonName.ReplaceAllString(s, "_"), "_") }

// usernameBase makes a blog username (3-32 of letters, digits, _) from the
// old display name, else from the email's local part.
func usernameBase(name, email string) string {
	s := sanitizeName(name)
	if len(s) < 3 {
		local, _, _ := strings.Cut(email, "@")
		s = sanitizeName(local)
	}
	for len(s) < 3 {
		s += "_x"
	}
	return s[:min(len(s), 32)]
}

// usernameTry is the i-th candidate for base: base, base_2, base_3, ...
func usernameTry(base string, i int) string {
	if i == 0 {
		return base
	}
	suffix := "_" + strconv.Itoa(i+1)
	return base[:min(len(base), 32-len(suffix))] + suffix
}

// argon2idPHC matches the hash the old server (node argon2) stores; Guard
// verifies the same format.
var argon2idPHC = regexp.MustCompile(`^\$argon2id\$v=\d+\$m=\d+,t=\d+,p=\d+\$[A-Za-z0-9+/]+\$[A-Za-z0-9+/]+$`)

func (im *importer) importUsers(ctx context.Context) error {
	rows, err := im.conv.sel(ctx, "users", "")
	if err != nil {
		return err
	}
	existing, err := im.st.Users(ctx)
	if err != nil {
		return err
	}
	placeholder := 0
	admins := 0
	for _, r := range rows {
		id, email := r.str("id"), strings.ToLower(strings.TrimSpace(r.str("email")))
		if r.flag("is_admin", false) {
			admins++
		}
		if n, ok := existing[email]; ok {
			im.users[id] = n
			im.rep.imported["user (matched)"]++
			continue
		}
		placeholder--
		switch {
		case im.assumeUsers:
			im.users[id] = placeholder
			im.rep.imported["user (assumed)"]++
		case !im.createMissing:
			im.rep.problem("user", id, "no account with email "+email+"; their data is skipped (-create-missing makes one)")
		case im.dry:
			im.users[id] = placeholder
			im.rep.imported["user (created)"]++
		default:
			n, err := im.createUser(ctx, r)
			if err != nil {
				im.rep.problem("user", id, "create "+email+": "+err.Error())
				continue
			}
			im.users[id] = n
			existing[email] = n
			im.rep.imported["user (created)"]++
			im.logf("created user %s (%s)", email, id)
		}
	}
	if admins > 0 {
		im.rep.warn("user", "-", fmt.Sprintf("%d old admin(s); admin rights are not carried over (use cmd/createadmin)", admins))
	}
	im.logf("users: %d in the old database, %d mapped", len(rows), len(im.users))
	return nil
}

func (im *importer) createUser(ctx context.Context, r Row) (int, error) {
	u := newUser{Email: strings.TrimSpace(r.str("email")), Name: r.str("name")}
	if h := r.str("password_hash"); argon2idPHC.MatchString(h) {
		u.PasswordHash = h
	}
	base := usernameBase(u.Name, u.Email)
	var err error
	for i := range 50 {
		u.Username = usernameTry(base, i)
		var n int
		if n, err = im.st.CreateUser(ctx, u); !errors.Is(err, domain.ErrConflict) {
			return n, err
		}
	}
	return 0, err
}

func (im *importer) importGlobal(ctx context.Context) error {
	g := &globalBundle{}
	if err := im.assets(ctx, g); err != nil {
		return err
	}
	rows, err := im.conv.sel(ctx, "user_prefs", "")
	if err != nil {
		return err
	}
	for _, r := range rows {
		n, ok := im.users[r.str("user_id")]
		switch {
		case !ok:
			im.rep.problem("prefs", r.str("user_id"), "user has no account here")
		case !jsonOK([]byte(r.str("data"))):
			im.rep.problem("prefs", r.str("user_id"), "data is not storable JSON")
		default:
			g.Prefs = append(g.Prefs, dgPrefs{n, json.RawMessage(r.str("data")), r.ts("updated_at", im.now)})
		}
	}
	if err := im.tokens(ctx, g); err != nil {
		return err
	}
	if err := im.audit(ctx, g); err != nil {
		return err
	}
	if im.dry {
		im.rep.imported["asset"] += len(g.Assets)
		im.rep.imported["prefs"] += len(g.Prefs)
		im.rep.imported["access token"] += len(g.Tokens)
		im.rep.imported["mcp audit"] += len(g.Audit)
		return nil
	}
	got, err := im.st.ImportGlobal(ctx, g)
	if err != nil {
		return err
	}
	for entity, total := range map[string]int{"asset": len(g.Assets), "prefs": len(g.Prefs), "access token": len(g.Tokens), "mcp audit": len(g.Audit)} {
		im.rep.imported[entity] += got[entity]
		im.rep.existing[entity] += total - got[entity]
	}
	return nil
}

func (im *importer) assets(ctx context.Context, g *globalBundle) error {
	rows, err := im.conv.sel(ctx, "assets", "")
	if err != nil {
		return err
	}
	for i, r := range rows {
		hash := r.str("hash")
		if !diagram.ValidHash(hash) {
			im.rep.problem("asset", hash, "not a lowercase sha256 hex")
			continue
		}
		if im.blobsIn == nil {
			im.rep.problem("asset", hash, "no old storage configured (-storage-dir / -old-s3-bucket)")
			continue
		}
		data, err := im.blobsIn.Get(ctx, hash)
		switch {
		case errors.Is(err, errBlobMissing):
			im.rep.problem("asset", hash, "blob missing in the old storage")
			continue
		case err != nil:
			im.rep.problem("asset", hash, "read blob: "+err.Error())
			continue
		case diagram.HashBytes(data) != hash:
			im.rep.problem("asset", hash, "blob content does not match its hash")
			continue
		}
		mime := diagram.DetectMime(data)
		if mime == "" {
			mime = r.str("mime")
		}
		if im.blobsOut != nil {
			if err := im.putBlob(ctx, hash, data, mime); err != nil {
				return fmt.Errorf("upload %s: %w", hash, err)
			}
		}
		g.Assets = append(g.Assets, dgAsset{hash, mime, len(data), im.userp(r.strp("uploaded_by")), r.ts("created_at", im.now)})
		if (i+1)%50 == 0 {
			im.logf("assets: %d/%d", i+1, len(rows))
		}
	}
	return nil
}

func (im *importer) userp(id *string) *int { return im.conv.userp(id) }

// putBlob uploads unless the object is already there (keys are content
// addressed, so a present object is the same bytes).
func (im *importer) putBlob(ctx context.Context, hash string, data []byte, mime string) error {
	if obj, err := im.blobsOut.Get(ctx, blobKey(hash)); err == nil {
		obj.Body.Close()
		return nil
	} else if !errors.Is(err, domain.ErrNotFound) {
		return err
	}
	return im.blobsOut.Put(ctx, blobKey(hash), bytes.NewReader(data), int64(len(data)), mime)
}

func (im *importer) tokens(ctx context.Context, g *globalBundle) error {
	rows, err := im.conv.sel(ctx, "access_tokens", "")
	if err != nil {
		return err
	}
	for _, r := range rows {
		id := r.str("id")
		n, ok := im.users[r.str("user_id")]
		hash := r.str("token_hash")
		if !ok {
			im.rep.problem("access token", id, "user has no account here")
			continue
		}
		if len(hash) != 64 {
			im.rep.problem("access token", id, "token_hash is not a sha256 hex digest")
			continue
		}
		var scopes []string
		if json.Unmarshal([]byte(r.str("scopes")), &scopes) != nil {
			im.rep.problem("access token", id, "scopes is not a JSON array")
			continue
		}
		keep := scopes[:0:0]
		for _, s := range scopes {
			if slices.Contains(diagram.Scopes, s) {
				keep = append(keep, s)
			} else {
				im.rep.warn("access token", id, "unknown scope "+s+" dropped")
			}
		}
		t := dgToken{ID: id, UserID: n, Name: r.str("name"), Hash: hash, Prefix: r.str("prefix"), Scopes: slices.Clone(keep),
			ExpiresAt: r.tsp("expires_at"), LastUsedAt: r.tsp("last_used_at"), RevokedAt: r.tsp("revoked_at"), CreatedAt: r.ts("created_at", im.now)}
		if t.Scopes == nil {
			t.Scopes = []string{}
		}
		if p := r.strp("project_ids"); p != nil {
			if json.Unmarshal([]byte(*p), &t.ProjectIDs) != nil {
				// a token limited to projects must never widen to all of them
				im.rep.problem("access token", id, "project_ids is not a JSON array")
				continue
			}
			if t.ProjectIDs == nil {
				t.ProjectIDs = []string{}
			}
		}
		g.Tokens = append(g.Tokens, t)
	}
	return nil
}

func (im *importer) audit(ctx context.Context, g *globalBundle) error {
	rows, err := im.conv.sel(ctx, "mcp_audit", "")
	if err != nil {
		return err
	}
	for _, r := range rows {
		n, ok := im.users[r.str("user_id")]
		if !ok {
			im.rep.problem("mcp audit", r.str("id"), "user has no account here")
			continue
		}
		a := dgAudit{ID: r.str("id"), UserID: n, TokenID: r.strp("token_id"), Tool: r.str("tool"), ProjectID: r.strp("project_id"),
			FileID: r.strp("file_id"), Status: r.str("status"), Detail: r.strp("detail"), CreatedAt: r.ts("created_at", im.now)}
		if d, ok := r.i64("duration_ms"); ok {
			v := int(d)
			a.DurationMs = &v
		}
		g.Audit = append(g.Audit, a)
	}
	return nil
}

func (im *importer) importProjects(ctx context.Context) error {
	rows, err := im.conv.sel(ctx, "projects", "")
	if err != nil {
		return err
	}
	for i, pr := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		id := pr.str("id")
		tag := fmt.Sprintf("[%d/%d] project %s %q", i+1, len(rows), id, pr.str("name"))
		owner, ok := im.users[pr.str("owner_id")]
		if !ok {
			im.rep.problem("project", id, "owner "+pr.str("owner_id")+" has no account here; project skipped")
			im.logf("%s: skipped (owner unmapped)", tag)
			continue
		}
		exists, err := im.st.ProjectExists(ctx, id)
		if err != nil {
			return err
		}
		if exists {
			im.rep.existing["project"]++
			im.logf("%s: already imported", tag)
			continue
		}
		b, err := im.conv.project(ctx, pr, owner)
		if err != nil {
			return fmt.Errorf("read project %s: %w", id, err)
		}
		if !im.dry {
			if err := im.st.ImportProject(ctx, b); err != nil {
				im.rep.failed = append(im.rep.failed, id)
				im.rep.warn("project", id, "transaction rolled back: "+err.Error())
				im.logf("%s: FAILED: %v", tag, err)
				continue
			}
		}
		im.rep.merge(b)
		im.logf("%s: %d files, %d versions, %d comments", tag, b.counts["file"], b.counts["version"], b.counts["comment"])
	}
	return nil
}
