package http

// Sharing in the diagram API: members, share links, assets and thumbnails,
// the project library and private stickies. Same wire conventions as
// diagram.go.

import (
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"time"

	"go.uber.org/zap"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/infrastructure/images"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/interfaces/http/middleware"
)

func (h *Handler) sharingRoutes(mux dgRouter) {
	p := DiagramPrefix
	mux.HandleFunc("GET "+p+"/projects/{id}/members", h.dgMembers)
	mux.HandleFunc("POST "+p+"/projects/{id}/members", h.dgAddMember)
	mux.HandleFunc("PATCH "+p+"/projects/{id}/members/{userId}", h.dgSetMember)
	mux.HandleFunc("DELETE "+p+"/projects/{id}/members/{userId}", h.dgRemoveMember)
	mux.HandleFunc("GET "+p+"/files/{id}/members", h.dgFileMembers)
	mux.HandleFunc("PUT "+p+"/files/{id}/members/{userId}", h.dgSetFileMember)
	mux.HandleFunc("DELETE "+p+"/files/{id}/members/{userId}", h.dgRemoveFileMember)

	mux.HandleFunc("GET "+p+"/projects/{id}/shares", h.dgShares)
	mux.HandleFunc("POST "+p+"/projects/{id}/shares", h.dgCreateShare)
	mux.HandleFunc("DELETE "+p+"/projects/{id}/shares/{shareId}", h.dgRevokeShare)
	mux.HandleFunc("GET "+p+"/share/{token}", h.dgOpenShare)
	mux.HandleFunc("PUT "+p+"/share/{token}", h.dgSaveShare)
	mux.HandleFunc("GET "+p+"/share/{token}/files/{fileId}", h.dgShareFile)
	mux.HandleFunc("PUT "+p+"/share/{token}/files/{fileId}", h.dgSaveShare)

	mux.HandleFunc("POST "+p+"/assets", h.dgUploadAsset)
	mux.HandleFunc("GET "+p+"/assets/{hash}", h.dgAsset)
	mux.HandleFunc("GET "+p+"/files/{id}/thumbnail", h.dgGetThumbnail)
	mux.HandleFunc("PUT "+p+"/files/{id}/thumbnail", h.dgPutThumbnail)

	mux.HandleFunc("GET "+p+"/projects/{id}/library", h.dgGetLibrary)
	mux.HandleFunc("PUT "+p+"/projects/{id}/library", h.dgPutLibrary)

	mux.HandleFunc("GET "+p+"/files/{id}/private", h.dgGetPrivate)
	mux.HandleFunc("PUT "+p+"/files/{id}/private", h.dgPutPrivate)
}

// dgActor is the caller's id (0 when anonymous) and the X-Share-Token
// header, or a 401 when there is neither.
func (h *Handler) dgActor(w http.ResponseWriter, r *http.Request) (int, string, bool) {
	uid, token := h.viewerID(r), r.Header.Get("X-Share-Token")
	if uid == 0 && token == "" {
		dgFail(w, r, http.StatusUnauthorized, "UNAUTHENTICATED", "Authentication required", nil)
		return 0, "", false
	}
	return uid, token, true
}

// dgUserParam reads the {userId} path value; an id that is no account id
// cannot be a member.
func dgUserParam(w http.ResponseWriter, r *http.Request) (int, bool) {
	n, err := strconv.Atoi(r.PathValue("userId"))
	if err != nil || n < 1 {
		dgError(w, r, diagram.NotFound("Member"))
		return 0, false
	}
	return n, true
}

// Members

type dgMember struct {
	UserID          string `json:"userId"`
	Email           string `json:"email"`
	Name            string `json:"name"`
	Role            string `json:"role"`
	CreatedAt       int64  `json:"createdAt"`
	IsOriginalOwner *bool  `json:"isOriginalOwner,omitempty"`
}

func memberJSON(m diagram.Member, owner *bool) dgMember {
	return dgMember{strconv.Itoa(m.UserID), m.Email, m.Name, string(m.Role), ms(m.CreatedAt), owner}
}

func membersJSON(ms []diagram.Member, withOwnerFlag bool) []dgMember {
	out := make([]dgMember, len(ms))
	for i, m := range ms {
		var flag *bool
		if withOwnerFlag {
			flag = &m.IsOriginalOwner
		}
		out[i] = memberJSON(m, flag)
	}
	return out
}

// dgMembers lists a project's members, its original owner first (role
// "owner", isOriginalOwner true). Response: {"items"}.
//
// spector:tags diagrams
func (h *Handler) dgMembers(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	items, err := h.Diagrams.Members(r.Context(), uid, id)
	if err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusOK, map[string]any{"items": membersJSON(items, true)})
}

// dgAddMember invites an existing account. Body: {"email", "role"}; email
// may also be a username, and "username" is accepted instead. Role is
// viewer, commenter or editor; inviting a member again changes the role. 404
// USER_NOT_FOUND for an unknown account, 400 OWNER_IMMUTABLE for the owner.
// Auth: owner. Response 201: {"member": {userId, email, name, role}}.
//
// spector:tags diagrams
func (h *Handler) dgAddMember(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok || !h.dgAllow(w, r, "dg-member-invite", 30, time.Hour) {
		return
	}
	var b struct {
		Email    string `json:"email"`
		Username string `json:"username"`
		Role     string `json:"role"`
	}
	if !dgDecode(w, r, &b, smallBody, false) {
		return
	}
	login := b.Email
	if login == "" {
		login = b.Username
	}
	m, err := h.Diagrams.AddMember(r.Context(), uid, id, login, diagram.Role(b.Role))
	if err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusCreated, map[string]any{"member": map[string]string{
		"userId": strconv.Itoa(m.UserID), "email": m.Email, "name": m.Name, "role": string(m.Role)}})
}

// dgSetMember changes a member's role. Body: {"role"}. Auth: owner.
//
// spector:tags diagrams
func (h *Handler) dgSetMember(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	member, ok := dgUserParam(w, r)
	if !ok {
		return
	}
	var b struct {
		Role string `json:"role"`
	}
	if !dgDecode(w, r, &b, smallBody, false) {
		return
	}
	if err := h.Diagrams.SetMemberRole(r.Context(), uid, id, member, diagram.Role(b.Role)); err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusOK, map[string]any{"member": map[string]string{"userId": strconv.Itoa(member), "role": b.Role}})
}

// dgRemoveMember removes a member (owners), or leaves the project (the
// member itself).
//
// spector:tags diagrams
func (h *Handler) dgRemoveMember(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	member, ok := dgUserParam(w, r)
	if !ok {
		return
	}
	if err := h.Diagrams.RemoveMember(r.Context(), uid, id, member); err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusNoContent, nil)
}

// dgFileMembers lists a file's per-file role overrides. Auth: owner.
//
// spector:tags diagrams
func (h *Handler) dgFileMembers(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	items, err := h.Diagrams.FileMembers(r.Context(), uid, id)
	if err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusOK, map[string]any{"items": membersJSON(items, false)})
}

// dgSetFileMember overrides a project member's role for this file. Body:
// {"role": "viewer"|"commenter"|"editor"}. Auth: owner.
//
// spector:tags diagrams
func (h *Handler) dgSetFileMember(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	member, ok := dgUserParam(w, r)
	if !ok {
		return
	}
	var b struct {
		Role string `json:"role"`
	}
	if !dgDecode(w, r, &b, smallBody, false) {
		return
	}
	if err := h.Diagrams.SetFileMember(r.Context(), uid, id, member, diagram.Role(b.Role)); err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusOK, map[string]any{"member": map[string]string{"userId": strconv.Itoa(member), "role": b.Role}})
}

// dgRemoveFileMember removes an override: the project role applies again.
// Auth: owner.
//
// spector:tags diagrams
func (h *Handler) dgRemoveFileMember(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	member, ok := dgUserParam(w, r)
	if !ok {
		return
	}
	if err := h.Diagrams.RemoveFileMember(r.Context(), uid, id, member); err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusNoContent, nil)
}

// Share links

type dgShare struct {
	ID        string  `json:"id"`
	Role      string  `json:"role"`
	FileID    *string `json:"fileId"`
	ExpiresAt *int64  `json:"expiresAt"`
	RevokedAt *int64  `json:"revokedAt"`
	CreatedAt int64   `json:"createdAt"`
	CreatedBy *string `json:"createdBy"`
}

func shareJSON(s diagram.ShareLink) dgShare {
	out := dgShare{ID: s.ID, Role: string(s.Role), FileID: s.FileID, CreatedAt: ms(s.CreatedAt), CreatedBy: userRef(s.CreatedBy)}
	if s.ExpiresAt != nil {
		v := ms(*s.ExpiresAt)
		out.ExpiresAt = &v
	}
	if s.RevokedAt != nil {
		v := ms(*s.RevokedAt)
		out.RevokedAt = &v
	}
	return out
}

// dgCreateShare creates a link for the project, or for one file (fileId).
// Body: {"role": "viewer"|"commenter"|"editor", "expiresInSeconds"?: 60..31536000,
// "fileId"?}. Response 201: {"share", "token", "url"}: the token is shown
// only here, only its hash is stored. Auth: owner.
//
// spector:tags diagrams
func (h *Handler) dgCreateShare(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok || !h.dgAllow(w, r, "dg-share-create", 30, time.Hour) {
		return
	}
	var b struct {
		Role             string  `json:"role"`
		ExpiresInSeconds *int    `json:"expiresInSeconds"`
		FileID           *string `json:"fileId"`
	}
	if !dgDecode(w, r, &b, smallBody, false) {
		return
	}
	s, token, err := h.Diagrams.CreateShare(r.Context(), uid, id, application.NewShare{Role: diagram.Role(b.Role), ExpiresInSeconds: b.ExpiresInSeconds, FileID: b.FileID})
	if err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusCreated, map[string]any{"share": shareJSON(s), "token": token, "url": DiagramPrefix + "/share/" + token})
}

// dgShares lists a project's links, newest first, without tokens.
// Auth: owner.
//
// spector:tags diagrams
func (h *Handler) dgShares(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	links, err := h.Diagrams.Shares(r.Context(), uid, id)
	if err != nil {
		dgError(w, r, err)
		return
	}
	items := make([]dgShare, len(links))
	for i, s := range links {
		items[i] = shareJSON(s)
	}
	dgWrite(w, http.StatusOK, map[string]any{"items": items})
}

// dgRevokeShare revokes a link; it stops working at once. Auth: owner.
//
// spector:tags diagrams
func (h *Handler) dgRevokeShare(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	shareID, ok := dgID(w, r, "shareId")
	if !ok {
		return
	}
	if err := h.Diagrams.RevokeShare(r.Context(), uid, id, shareID); err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusNoContent, nil)
}

// dgToken reads the {token} path value (16-128 characters of A-Z a-z 0-9 _ -).
func dgToken(w http.ResponseWriter, r *http.Request) (string, bool) {
	t := r.PathValue("token")
	if !diagram.ValidShareToken(t) {
		dgValidation(w, r, "params", "token: must be 16-128 characters of A-Z a-z 0-9 _ -")
		return "", false
	}
	return t, true
}

// dgOpenShare opens a link; no session is involved. A file link answers
// {"share", "project", "file"} (ETag is the version), a project link
// {"share", "project", "treeRev", "folders", "files"}. 404 for an unknown or
// revoked link, 410 SHARE_EXPIRED for an expired one.
//
// spector:tags diagrams
func (h *Handler) dgOpenShare(w http.ResponseWriter, r *http.Request) {
	token, ok := dgToken(w, r)
	if !ok {
		return
	}
	v, err := h.Diagrams.OpenShare(r.Context(), token)
	if err != nil {
		dgError(w, r, err)
		return
	}
	scope := "project"
	if v.Link.FileID != nil {
		scope = "file"
	}
	proj := projectJSON(v.Project)
	proj.OwnerID = ""
	out := map[string]any{"share": map[string]any{"scope": scope, "role": string(v.Link.Role), "fileId": v.Link.FileID}, "project": proj}
	if v.File != nil {
		etag(w, v.File.Version)
		out["file"] = docJSON(*v.File)
	} else {
		out["treeRev"] = v.Project.TreeRev
		out["folders"] = foldersJSON(v.Folders)
		files := make([]dgFile, len(v.Files))
		for i, f := range v.Files {
			files[i] = fileJSON(f, v.Link.Role)
		}
		out["files"] = files
	}
	dgWrite(w, http.StatusOK, out)
}

// dgShareFile opens one file through a link: the link's own file, or any
// file of the project for a project link.
//
// spector:tags diagrams
func (h *Handler) dgShareFile(w http.ResponseWriter, r *http.Request) {
	token, ok := dgToken(w, r)
	if !ok {
		return
	}
	fileID, ok := dgID(w, r, "fileId")
	if !ok {
		return
	}
	f, err := h.Diagrams.ShareFile(r.Context(), token, fileID)
	if err != nil {
		dgError(w, r, err)
		return
	}
	etag(w, f.Version)
	dgWrite(w, http.StatusOK, map[string]any{"file": docJSON(f)})
}

// dgSaveShare saves through an editor link, like dgSaveFile (If-Match
// required, 412 on conflict). On /share/{token} the link must be a file
// link; on /share/{token}/files/{fileId} a project link names the file.
// 403 for a viewer link.
//
// spector:tags diagrams
func (h *Handler) dgSaveShare(w http.ResponseWriter, r *http.Request) {
	token, ok := dgToken(w, r)
	if !ok {
		return
	}
	fileID := r.PathValue("fileId")
	if fileID != "" {
		if fileID, ok = dgID(w, r, "fileId"); !ok {
			return
		}
	}
	var b struct {
		Name *string         `json:"name"`
		Data json.RawMessage `json:"data"`
	}
	if !dgDecode(w, r, &b, h.docLimit(), false) {
		return
	}
	f, err := h.Diagrams.SaveShare(r.Context(), token, fileID, b.Name, b.Data, r.Header.Get("If-Match"))
	if err != nil {
		dgError(w, r, err)
		return
	}
	etag(w, f.Version)
	dgWrite(w, http.StatusOK, map[string]any{"file": docJSON(f)})
}

// Assets and thumbnails

// assetUploadsPerMinute is how many images one user may upload a minute.
const assetUploadsPerMinute = 60

// dgAllow applies a per-user rate limit (the Limiter in Deps; none: no
// limit) and answers 429 RATE_LIMITED, with Retry-After, when exceeded.
func (h *Handler) dgAllow(w http.ResponseWriter, r *http.Request, name string, n int, window time.Duration) bool {
	if h.Limit == nil {
		return true
	}
	ok, retry := h.Limit(r.Context(), name+":user:"+strconv.Itoa(h.viewerID(r)), n, window)
	if ok {
		return true
	}
	secs := max(1, int((retry+time.Second-1)/time.Second))
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	dgFail(w, r, http.StatusTooManyRequests, "RATE_LIMITED", "Too many requests. Retry in "+strconv.Itoa(secs)+"s.",
		map[string]int{"retryAfterSeconds": secs})
	return false
}

// dgUploadAsset stores an image content-addressed by its sha256.
//
// Multipart field "file", first in the body. PNG, JPEG, WebP or SVG, detected
// from the bytes (SVG is sanitized first; one that cannot be cleaned: 415).
// A PNG, JPEG or WebP is stored as WebP, so hash, mime and size describe the
// converted file. Over the size limit: 413. Response:
// {"asset": {hash, mime, size, url, deduped}}, 201 for new bytes and 200
// when the same bytes were stored before. Auth: any user; at most 60
// uploads a minute (429).
//
// spector:tags diagrams
func (h *Handler) dgUploadAsset(w http.ResponseWriter, r *http.Request) {
	uid, ok := h.dgUser(w, r)
	if !ok || !h.dgAllow(w, r, "dg-asset", assetUploadsPerMinute, time.Minute) {
		return
	}
	limit := int64(h.Diagrams.MaxAssetBytes)
	r.Body = http.MaxBytesReader(w, r.Body, limit+smallBody)
	mr, err := r.MultipartReader()
	if err != nil {
		dgError(w, r, diagram.UnsupportedMedia("Send multipart/form-data with a \"file\" field"))
		return
	}
	part, err := mr.NextPart()
	if err != nil || part.FormName() != "file" {
		if tooLarge(err) {
			dgError(w, r, diagram.TooLarge("The file is too large"))
			return
		}
		dgError(w, r, diagram.MissingFile())
		return
	}
	data, err := readPart(part, limit)
	switch {
	case errors.Is(err, errPartTooLarge) || tooLarge(err):
		dgError(w, r, diagram.TooLarge("The file is too large"))
		return
	case err != nil:
		dgFail(w, r, http.StatusBadRequest, "INVALID_MULTIPART", "Request body is not valid multipart/form-data", nil)
		return
	}
	// PNG, JPEG and WebP images are stored as WebP (smaller, no metadata); SVG and anything that cannot be
	// converted stay as sent, and StoreAsset still decides what is accepted.
	switch diagram.DetectMime(data) {
	case diagram.MimePNG, diagram.MimeJPEG, diagram.MimeWebP:
		if out, err := images.ToWebP(data); err == nil {
			data = out
		}
	}
	a, err := h.Diagrams.StoreAsset(r.Context(), uid, data)
	if err != nil {
		dgError(w, r, err)
		return
	}
	code := http.StatusCreated
	if a.Deduped {
		code = http.StatusOK
	}
	dgWrite(w, code, map[string]any{"asset": map[string]any{"hash": a.Hash, "mime": a.Mime, "size": a.Size,
		"url": DiagramPrefix + "/assets/" + a.Hash, "deduped": a.Deduped}})
}

var errPartTooLarge = errors.New("part too large")

// readPart reads a multipart part of at most limit bytes.
func readPart(p *multipart.Part, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(p, limit+1))
	if err == nil && int64(len(data)) > limit {
		err = errPartTooLarge
	}
	return data, err
}

func tooLarge(err error) bool {
	var big *http.MaxBytesError
	return errors.As(err, &big)
}

// dgAsset serves an asset by its content hash. Public on purpose: the hash
// is unguessable and <img> tags cannot send credentials.
//
// Auth: none.
//
// spector:tags diagrams
func (h *Handler) dgAsset(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	a, err := h.Diagrams.AssetMeta(r.Context(), hash)
	if err != nil {
		dgError(w, r, err)
		return
	}
	hd := w.Header()
	hd.Set("ETag", `"`+hash+`"`)
	hd.Set("Cache-Control", "public, max-age=31536000, immutable")
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Cross-Origin-Resource-Policy", "cross-origin")
	if a.Mime == diagram.MimeSVG {
		// Belt and braces on top of the sanitizer: even if a script slipped
		// through, opened directly it runs in an opaque origin with no
		// network and no script.
		hd.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	}
	h.serveBlob(w, r, hash, a.Mime)
}

// serveBlob answers 304 for a matching If-None-Match, else streams the
// blob of an asset with its type.
func (h *Handler) serveBlob(w http.ResponseWriter, r *http.Request, hash, mime string) {
	if inm := r.Header.Get("If-None-Match"); inm == `"`+hash+`"` {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	obj, err := h.Diagrams.AssetBlob(r.Context(), hash)
	if err != nil {
		dgError(w, r, err)
		return
	}
	defer obj.Body.Close()
	w.Header().Set("Content-Type", mime)
	if obj.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(obj.Size, 10))
	}
	if _, err := io.Copy(w, obj.Body); err != nil {
		zap.L().Warn("write blob", zap.Error(err))
	}
}

// dgGetThumbnail serves a file's preview image. It needs read access to the
// file (a session or an X-Share-Token) and is cached privately.
//
// spector:tags diagrams
func (h *Handler) dgGetThumbnail(w http.ResponseWriter, r *http.Request) {
	uid, token, ok := h.dgActor(w, r)
	if !ok {
		return
	}
	id, ok := dgID(w, r, "id")
	if !ok {
		return
	}
	a, err := h.Diagrams.Thumbnail(r.Context(), uid, token, id)
	if err != nil {
		dgError(w, r, err)
		return
	}
	hd := w.Header()
	hd.Set("ETag", `"`+a.Hash+`"`)
	hd.Set("Cache-Control", "private, max-age=3600")
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Vary", "Cookie, Authorization, X-Share-Token")
	h.serveBlob(w, r, a.Hash, a.Mime)
}

// dgPutThumbnail sets the file's preview: the raw image as the body (PNG or
// WebP, at most 200 KB, detected from the bytes). It does not change the
// file's version. Response: {"thumbnailUrl"}. Auth: editor.
//
// spector:tags diagrams
func (h *Handler) dgPutThumbnail(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, application.MaxThumbnailBytes+64<<10))
	if tooLarge(err) {
		dgError(w, r, diagram.TooLarge("The thumbnail may be at most 200 KB"))
		return
	}
	if err != nil {
		dgFail(w, r, http.StatusBadRequest, "BAD_REQUEST", "Could not read the request body", nil)
		return
	}
	hash, err := h.Diagrams.SetThumbnail(r.Context(), uid, id, data)
	if err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusOK, map[string]string{"thumbnailUrl": thumbURL(id, hash)})
}

// Library

func dgLibrary(l diagram.Library) map[string]any {
	return map[string]any{"rev": l.Rev, "doc": l.Doc}
}

// dgLibraryConflict is the answer to a stale library save: 412 with the
// current {rev, doc} next to the error, so the client can merge and retry.
func dgLibraryConflict(w http.ResponseWriter, r *http.Request, c *diagram.LibraryConflict) {
	var b dgErrorBody
	b.Error.Code, b.Error.Message = c.Code(), c.Error()
	b.Error.RequestID = middleware.RequestIDFrom(r.Context())
	dgWrite(w, http.StatusPreconditionFailed, map[string]any{"error": b.Error, "rev": c.Current.Rev, "doc": c.Current.Doc})
}

// dgGetLibrary returns the project library {"rev", "doc"}: rev 0 and an
// empty document when it was never saved. A session of any member, or an
// X-Share-Token of a project-wide link.
//
// spector:tags diagrams
func (h *Handler) dgGetLibrary(w http.ResponseWriter, r *http.Request) {
	uid, token, ok := h.dgActor(w, r)
	if !ok {
		return
	}
	id, ok := dgID(w, r, "id")
	if !ok {
		return
	}
	l, err := h.Diagrams.GetLibrary(r.Context(), uid, token, id)
	if err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusOK, dgLibrary(l))
}

// dgPutLibrary replaces the library. Body: {"baseRev", "doc"}; baseRev must
// be the current rev, else 412 LIBRARY_CONFLICT with the current {rev, doc}
// (the Node server answers 409). At most 2 MB. Auth: editor.
//
// spector:tags diagrams
func (h *Handler) dgPutLibrary(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	var b struct {
		BaseRev *int            `json:"baseRev"`
		Doc     json.RawMessage `json:"doc"`
	}
	if !dgDecode(w, r, &b, diagram.MaxLibraryBytes+4096, false) {
		return
	}
	if b.BaseRev == nil {
		dgValidation(w, r, "body", "baseRev: Required")
		return
	}
	if len(b.Doc) == 0 {
		dgValidation(w, r, "body", "doc: Required")
		return
	}
	l, err := h.Diagrams.SaveLibrary(r.Context(), uid, id, *b.BaseRev, b.Doc)
	if err != nil {
		dgError(w, r, err)
		return
	}
	dgWrite(w, http.StatusOK, dgLibrary(l))
}

// Private stickies

// privateJSON is {"nodes", "edges", "updatedAt"} (0 when there are none).
func privateJSON(p diagram.Private) map[string]json.RawMessage {
	var doc map[string]json.RawMessage
	if json.Unmarshal(p.Data, &doc) != nil || doc == nil {
		doc = map[string]json.RawMessage{"nodes": json.RawMessage("[]"), "edges": json.RawMessage("[]")}
	}
	var at int64
	if !p.UpdatedAt.IsZero() {
		at = ms(p.UpdatedAt)
	}
	doc["updatedAt"] = json.RawMessage(strconv.FormatInt(at, 10))
	return doc
}

// dgGetPrivate returns the caller's own private stickies of a file, never
// anybody else's: {"nodes", "edges", "updatedAt"}. Auth: viewer.
//
// spector:tags diagrams
func (h *Handler) dgGetPrivate(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	p, err := h.Diagrams.GetPrivate(r.Context(), uid, id)
	if err != nil {
		dgError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	dgWrite(w, http.StatusOK, privateJSON(p))
}

// dgPutPrivate replaces the caller's private stickies of a file. Body:
// {"nodes" (at most 500 stickies), "edges" (at most 1000)}; every sticky is
// stamped private and written by the caller. An empty set clears them. At
// most 1 MB (413). Auth: editor.
//
// spector:tags diagrams
func (h *Handler) dgPutPrivate(w http.ResponseWriter, r *http.Request) {
	uid, id, ok := h.dgReq(w, r)
	if !ok {
		return
	}
	var body json.RawMessage
	if !dgDecode(w, r, &body, diagram.MaxPrivateBytes+64<<10, false) {
		return
	}
	p, err := h.Diagrams.PutPrivate(r.Context(), uid, id, body)
	if err != nil {
		dgError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	dgWrite(w, http.StatusOK, privateJSON(p))
}
