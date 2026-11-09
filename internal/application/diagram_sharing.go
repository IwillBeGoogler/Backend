package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/user"
)

// DiagramUsers is what the diagram use cases need from the accounts: the
// existing user repository satisfies it.
type DiagramUsers interface {
	GetByID(ctx context.Context, id int) (user.User, error)
	GetByUsername(ctx context.Context, username string) (user.User, error)
	// GetByLogin finds a user by username or email.
	GetByLogin(ctx context.Context, login string) (user.User, error)
}

// Members, share links, assets, the library and private stickies of the
// diagram workspace. They follow the same rules as the rest of it: a
// stranger gets not found, a role below the minimum gets forbidden.

// shareAccess is the access of a share link: it goes through the same role
// checks as a member (diagram.Role.Allows).
type shareAccess struct {
	link    diagram.ShareLink
	project diagram.Project
	role    diagram.Role
}

// resolveShare looks up a share link by its plaintext token: not found for
// an unknown or revoked link, gone for an expired one. Only the hash is
// stored; it is compared in constant time on top of the unique index lookup.
func (d *Diagrams) resolveShare(ctx context.Context, token string) (shareAccess, error) {
	if !diagram.ValidShareToken(token) {
		return shareAccess{}, diagram.NotFound("Share link")
	}
	hash := diagram.HashToken(token)
	link, err := d.Sharing.ShareByHash(ctx, hash)
	if errors.Is(err, domain.ErrNotFound) || err == nil && !diagram.HashEqual(link.TokenHash, hash) {
		return shareAccess{}, diagram.NotFound("Share link")
	}
	if err != nil {
		return shareAccess{}, err
	}
	if link.RevokedAt != nil {
		return shareAccess{}, diagram.NotFound("Share link")
	}
	if link.Expired(d.clock()) {
		return shareAccess{}, diagram.ShareExpired()
	}
	p, err := d.Repo.Project(ctx, link.ProjectID)
	if errors.Is(err, domain.ErrNotFound) {
		return shareAccess{}, diagram.NotFound("Share link")
	}
	return shareAccess{link, p, link.Role}, err
}

// fileAccessVia is fileAccess for a signed-in user (actor, 0 when
// anonymous) who may also hold a share token. A member goes first; the
// token is tried only for someone who is not a member. A project link
// covers every file of the project, a file link its own file.
func (d *Diagrams) fileAccessVia(ctx context.Context, actor int, token, id string, min diagram.Role) (access, diagram.Doc, error) {
	if actor != 0 {
		a, f, err := d.fileAccess(ctx, actor, id, min)
		if err == nil || token == "" || !errors.Is(err, domain.ErrNotFound) {
			return a, f, err
		}
	} else if token == "" {
		return access{}, diagram.Doc{}, domain.ErrUnauthorized
	}
	sa, err := d.resolveShare(ctx, token)
	if err != nil {
		return access{}, diagram.Doc{}, err
	}
	f, err := d.Repo.File(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return access{}, f, diagram.NotFound("File")
	}
	if err != nil {
		return access{}, f, err
	}
	if f.ProjectID != sa.project.ID || sa.link.FileID != nil && *sa.link.FileID != id {
		return access{}, f, diagram.NotFound("File")
	}
	if !sa.role.Allows(min) {
		return access{}, f, diagram.NeedRole(min)
	}
	return access{sa.project, sa.role}, f, nil
}

// projectAccessVia is projectAccess with a share token as the fallback; only
// a project-wide link opens project-level routes.
func (d *Diagrams) projectAccessVia(ctx context.Context, actor int, token, id string, min diagram.Role) (access, error) {
	if actor != 0 {
		a, err := d.projectAccess(ctx, actor, id, min)
		if err == nil || token == "" || !errors.Is(err, domain.ErrNotFound) {
			return a, err
		}
	} else if token == "" {
		return access{}, domain.ErrUnauthorized
	}
	sa, err := d.resolveShare(ctx, token)
	if err != nil {
		return access{}, err
	}
	if sa.project.ID != id || sa.link.FileID != nil {
		return access{}, diagram.NotFound("Project")
	}
	if !sa.role.Allows(min) {
		return access{}, diagram.NeedRole(min)
	}
	return access{sa.project, sa.role}, nil
}

// GetFileVia is GetFile for a caller who may hold a share token instead of
// (or besides) an account.
func (d *Diagrams) GetFileVia(ctx context.Context, actor int, token, id string) (DiagramFile, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.GetFileVia")
	defer span.End()
	a, f, err := d.fileAccessVia(ctx, actor, token, id, diagram.RoleViewer)
	return DiagramFile{f, a.role}, err
}

// Members

// name is how a person is shown: the display name, else the username.
func accountName(u user.User) string {
	if u.DisplayName != "" {
		return u.DisplayName
	}
	return u.Username
}

// fill adds the account's email and name to members; an account that is
// gone leaves them empty.
func (d *Diagrams) fill(ctx context.Context, ms []diagram.Member) error {
	for i := range ms {
		u, err := d.Users.GetByID(ctx, ms[i].UserID)
		if errors.Is(err, domain.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		ms[i].Email, ms[i].Name = u.Email, accountName(u)
	}
	return nil
}

// Members lists a project's members, its original owner first.
func (d *Diagrams) Members(ctx context.Context, actor int, projectID string) ([]diagram.Member, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.Members")
	defer span.End()
	a, err := d.projectAccess(ctx, actor, projectID, diagram.RoleViewer)
	if err != nil {
		return nil, err
	}
	rows, err := d.Sharing.Members(ctx, projectID)
	if err != nil {
		return nil, err
	}
	owner := diagram.Member{UserID: a.project.OwnerID, Role: diagram.RoleOwner, CreatedAt: a.project.CreatedAt, IsOriginalOwner: true}
	out := append([]diagram.Member{owner}, rows...)
	if err := d.fill(ctx, out); err != nil {
		return nil, err
	}
	if a.role != diagram.RoleOwner { // collaborators' emails are for owners only
		for i := range out {
			if out[i].UserID != actor {
				out[i].Email = ""
			}
		}
	}
	return out, nil
}

func checkMemberRole(role diagram.Role) error {
	if !role.OverrideRole() {
		return diagram.Validation("role: must be viewer, commenter or editor")
	}
	return nil
}

// AddMember invites an existing account, by email or username, with a role;
// inviting a member again changes the role. Owners only.
func (d *Diagrams) AddMember(ctx context.Context, actor int, projectID, login string, role diagram.Role) (diagram.Member, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.AddMember")
	defer span.End()
	login = strings.ToLower(strings.TrimSpace(login))
	if login == "" {
		return diagram.Member{}, diagram.Validation("email: required")
	}
	if err := checkMemberRole(role); err != nil {
		return diagram.Member{}, err
	}
	a, err := d.projectAccess(ctx, actor, projectID, diagram.RoleOwner)
	if err != nil {
		return diagram.Member{}, err
	}
	u, err := d.Users.GetByLogin(ctx, login)
	if errors.Is(err, domain.ErrNotFound) || err == nil && u.DeletedAt != nil {
		return diagram.Member{}, diagram.UserNotFound()
	}
	if err != nil {
		return diagram.Member{}, err
	}
	if u.ID == a.project.OwnerID {
		return diagram.Member{}, diagram.OwnerImmutable("The project's original owner cannot be given another role")
	}
	if err := d.Sharing.SetMember(ctx, projectID, u.ID, role, d.clock()); err != nil {
		return diagram.Member{}, err
	}
	return diagram.Member{UserID: u.ID, Email: u.Email, Name: accountName(u), Role: role}, nil
}

// SetMemberRole changes a member's role. Owners only.
func (d *Diagrams) SetMemberRole(ctx context.Context, actor int, projectID string, userID int, role diagram.Role) error {
	ctx, span := tracer.Start(ctx, "Diagrams.SetMemberRole")
	defer span.End()
	if err := checkMemberRole(role); err != nil {
		return err
	}
	a, err := d.projectAccess(ctx, actor, projectID, diagram.RoleOwner)
	if err != nil {
		return err
	}
	if userID == a.project.OwnerID {
		return diagram.OwnerImmutable("The project's original owner cannot be changed")
	}
	old, oldErr := d.Repo.MemberRole(ctx, projectID, userID)
	ok, err := d.Sharing.UpdateMember(ctx, projectID, userID, role)
	if err == nil && !ok {
		err = diagram.NotFound("Member")
	}
	// Unknown old role: revoke live channels anyway; they reconnect with the new role.
	if err == nil && (oldErr != nil || old != role && old.Rank() > role.Rank()) {
		d.memberLost(ctx, projectID, userID)
	}
	return err
}

// RemoveMember removes a member (owners), or lets a member leave (itself).
// The member's per-file overrides in the project go with it.
func (d *Diagrams) RemoveMember(ctx context.Context, actor int, projectID string, userID int) error {
	ctx, span := tracer.Start(ctx, "Diagrams.RemoveMember")
	defer span.End()
	a, err := d.projectAccess(ctx, actor, projectID, diagram.RoleViewer)
	if err != nil {
		return err
	}
	if userID != actor && a.role != diagram.RoleOwner {
		return diagram.Forbidden("Only owners can remove other members")
	}
	if userID == a.project.OwnerID {
		return diagram.OwnerImmutable("The project's original owner cannot be removed")
	}
	ok, err := d.Sharing.RemoveMember(ctx, projectID, userID)
	if err == nil && !ok {
		err = diagram.NotFound("Member")
	}
	if err == nil {
		d.memberLost(ctx, projectID, userID)
	}
	return err
}

// FileMembers lists a file's role overrides. Owners only.
func (d *Diagrams) FileMembers(ctx context.Context, actor int, fileID string) ([]diagram.Member, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.FileMembers")
	defer span.End()
	if _, _, err := d.fileAccess(ctx, actor, fileID, diagram.RoleOwner); err != nil {
		return nil, err
	}
	ms, err := d.Sharing.FileMembers(ctx, fileID)
	if err != nil {
		return nil, err
	}
	return ms, d.fill(ctx, ms)
}

// SetFileMember overrides a project member's role for one file (viewer or
// editor). Owners only; the owner itself cannot be overridden.
func (d *Diagrams) SetFileMember(ctx context.Context, actor int, fileID string, userID int, role diagram.Role) error {
	ctx, span := tracer.Start(ctx, "Diagrams.SetFileMember")
	defer span.End()
	if err := checkMemberRole(role); err != nil {
		return err
	}
	a, _, err := d.fileAccess(ctx, actor, fileID, diagram.RoleOwner)
	if err != nil {
		return err
	}
	if userID == a.project.OwnerID {
		return diagram.OwnerImmutable("The project's original owner cannot be overridden")
	}
	member, err := d.Repo.MemberRole(ctx, a.project.ID, userID)
	if err != nil {
		return err
	}
	if member == "" {
		return diagram.NotFound("Member")
	}
	old, err := d.Repo.FileRole(ctx, fileID, userID)
	if err != nil {
		return err
	}
	if old == "" {
		old = member
	}
	if err := d.Sharing.SetFileMember(ctx, fileID, userID, role, d.clock()); err != nil {
		return err
	}
	if old.Rank() > role.Rank() {
		d.memberLost(ctx, a.project.ID, userID)
	}
	return nil
}

// RemoveFileMember drops an override: the project role applies again.
func (d *Diagrams) RemoveFileMember(ctx context.Context, actor int, fileID string, userID int) error {
	ctx, span := tracer.Start(ctx, "Diagrams.RemoveFileMember")
	defer span.End()
	a, _, err := d.fileAccess(ctx, actor, fileID, diagram.RoleOwner)
	if err != nil {
		return err
	}
	over, err := d.Repo.FileRole(ctx, fileID, userID)
	if err != nil {
		return err
	}
	member, err := d.Repo.MemberRole(ctx, a.project.ID, userID)
	if err != nil {
		return err
	}
	ok, err := d.Sharing.RemoveFileMember(ctx, fileID, userID)
	if err == nil && !ok {
		err = diagram.NotFound("Override")
	}
	if err == nil && over.Rank() > member.Rank() { // the project role applies again, and it is lower
		d.memberLost(ctx, a.project.ID, userID)
	}
	return err
}

// Share links

// NewShare is a share link to create: the whole project, or FileID.
type NewShare struct {
	Role             diagram.Role
	ExpiresInSeconds *int
	FileID           *string
}

// CreateShare makes a link. The token is returned here and never again.
// Owners only.
func (d *Diagrams) CreateShare(ctx context.Context, actor int, projectID string, in NewShare) (diagram.ShareLink, string, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.CreateShare")
	defer span.End()
	if !in.Role.OverrideRole() {
		return diagram.ShareLink{}, "", diagram.Validation("role: must be viewer, commenter or editor")
	}
	if n := in.ExpiresInSeconds; n != nil && (*n < diagram.MinShareSeconds || *n > diagram.MaxShareSeconds) {
		return diagram.ShareLink{}, "", diagram.Validation("expiresInSeconds: must be between 60 and 31536000")
	}
	if in.FileID != nil {
		if *in.FileID == "" || len(*in.FileID) > 64 {
			return diagram.ShareLink{}, "", diagram.Validation("fileId: must be 1-64 characters")
		}
	}
	a, err := d.projectAccess(ctx, actor, projectID, diagram.RoleOwner)
	if err != nil {
		return diagram.ShareLink{}, "", err
	}
	if in.FileID != nil {
		f, err := d.Repo.File(ctx, *in.FileID)
		if errors.Is(err, domain.ErrNotFound) || err == nil && f.ProjectID != a.project.ID {
			return diagram.ShareLink{}, "", diagram.FileNotInProject()
		}
		if err != nil {
			return diagram.ShareLink{}, "", err
		}
	}
	token, hash := diagram.NewShareToken()
	now := d.clock()
	s := diagram.ShareLink{ID: diagram.NewID(diagram.PrefixShare), ProjectID: a.project.ID, FileID: in.FileID, Role: in.Role,
		CreatedBy: actor, CreatedAt: now, TokenHash: hash}
	if in.ExpiresInSeconds != nil {
		at := now.Add(time.Duration(*in.ExpiresInSeconds) * time.Second)
		s.ExpiresAt = &at
	}
	return s, token, d.Sharing.CreateShare(ctx, s)
}

// Shares lists a project's links, newest first, without tokens. Owners only.
func (d *Diagrams) Shares(ctx context.Context, actor int, projectID string) ([]diagram.ShareLink, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.Shares")
	defer span.End()
	if _, err := d.projectAccess(ctx, actor, projectID, diagram.RoleOwner); err != nil {
		return nil, err
	}
	return d.Sharing.Shares(ctx, projectID)
}

// RevokeShare stops a link from working at once. Owners only.
func (d *Diagrams) RevokeShare(ctx context.Context, actor int, projectID, id string) error {
	ctx, span := tracer.Start(ctx, "Diagrams.RevokeShare")
	defer span.End()
	if _, err := d.projectAccess(ctx, actor, projectID, diagram.RoleOwner); err != nil {
		return err
	}
	ok, err := d.Sharing.RevokeShare(ctx, projectID, id, d.clock())
	if err == nil && !ok {
		err = diagram.NotFound("Share link")
	}
	if err == nil {
		d.shareRevoked(ctx, projectID, id)
	}
	return err
}

// ShareView is what a link opens: the file (file links) or the project tree
// (project links).
type ShareView struct {
	Link    diagram.ShareLink
	Project diagram.ProjectSummary
	File    *DiagramFile
	Folders []diagram.Folder
	Files   []diagram.File
}

// shareFile is the file a link opens: its own file, or any file of the
// project for a project link (fileID names it; "" for a file link's own).
// ok is false when a project link names no file.
func (d *Diagrams) shareFile(ctx context.Context, sa shareAccess, fileID string) (diagram.Doc, bool, error) {
	if sa.link.FileID != nil && fileID != "" && fileID != *sa.link.FileID {
		return diagram.Doc{}, false, diagram.NotFound("File")
	}
	id := fileID
	if id == "" && sa.link.FileID != nil {
		id = *sa.link.FileID
	}
	if id == "" {
		return diagram.Doc{}, false, nil
	}
	f, err := d.Repo.File(ctx, id)
	if errors.Is(err, domain.ErrNotFound) || err == nil && f.ProjectID != sa.project.ID {
		return diagram.Doc{}, false, diagram.NotFound("File")
	}
	return f, err == nil, err
}

// OpenShare opens a link: a file link gives the file, a project link the
// tree (folders and file metadata).
func (d *Diagrams) OpenShare(ctx context.Context, token string) (ShareView, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.OpenShare")
	defer span.End()
	sa, err := d.resolveShare(ctx, token)
	if err != nil {
		return ShareView{}, err
	}
	v := ShareView{Link: sa.link}
	if sa.link.FileID != nil {
		f, _, err := d.shareFile(ctx, sa, "")
		if err != nil {
			return v, err
		}
		if v.Project, err = d.summary(ctx, sa.project, sa.role); err != nil {
			return v, err
		}
		v.File = &DiagramFile{f, sa.role}
		return v, nil
	}
	folders, files, err := d.Repo.Tree(ctx, sa.project.ID)
	if err != nil {
		return v, err
	}
	v.Project = diagram.ProjectSummary{Project: sa.project, Role: sa.role, FileCount: len(files), FolderCount: len(folders)}
	v.Folders, v.Files = folders, files
	return v, nil
}

// ShareFile opens one file through a link.
func (d *Diagrams) ShareFile(ctx context.Context, token, fileID string) (DiagramFile, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.ShareFile")
	defer span.End()
	sa, err := d.resolveShare(ctx, token)
	if err != nil {
		return DiagramFile{}, err
	}
	f, ok, err := d.shareFile(ctx, sa, fileID)
	if err == nil && !ok {
		err = diagram.NotFound("File")
	}
	return DiagramFile{f, sa.role}, err
}

// SaveShare saves a file through an editor link, with the same If-Match
// rules as SaveFile. The save is attributed to the link (not to a person). fileID is "" for a file link's own file.
func (d *Diagrams) SaveShare(ctx context.Context, token, fileID string, name *string, data json.RawMessage, ifMatch string) (DiagramFile, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.SaveShare")
	defer span.End()
	sa, err := d.resolveShare(ctx, token)
	if err != nil {
		return DiagramFile{}, err
	}
	if !sa.role.Allows(diagram.RoleEditor) {
		return DiagramFile{}, diagram.ReadOnlyShare()
	}
	f, ok, err := d.shareFile(ctx, sa, fileID)
	if err != nil {
		return DiagramFile{}, err
	}
	if !ok {
		return DiagramFile{}, diagram.NotFound("File")
	}
	if name == nil && len(data) == 0 {
		return DiagramFile{}, diagram.Validation("Provide name and/or data")
	}
	if name != nil {
		if err := diagram.CheckNameSize(*name, true); err != nil {
			return DiagramFile{}, err
		}
	}
	expected, ok := diagram.ParseIfMatch(ifMatch)
	if !ok {
		return DiagramFile{}, diagram.IfMatchRequired(f.Version)
	}
	doc, err := d.commit(ctx, writer{share: sa.link.ID, owner: sa.project.OwnerID}, f, expected, name, data, nil)
	return DiagramFile{doc, sa.role}, err
}

// Assets and thumbnails

// StoredAsset is an uploaded image; Deduped is true when the same bytes
// were stored before.
type StoredAsset struct {
	Hash    string
	Mime    string
	Size    int
	Deduped bool
}

// MaxThumbnailBytes is the largest file preview.
const MaxThumbnailBytes = 200 * 1024

// blobKey is the object key of an asset: the hash under its own prefix, so
// it cannot collide with (or be served as) a blog upload.
func blobKey(hash string) string { return "diagram/" + hash }

// StoreAsset stores an image content-addressed (by the sha256 of the
// bytes). The type is sniffed from the bytes and must be one of only (any
// accepted image when empty); the size is capped by MaxAssetBytes.
func (d *Diagrams) StoreAsset(ctx context.Context, actor int, data []byte, only ...string) (StoredAsset, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.StoreAsset")
	defer span.End()
	if len(data) > d.MaxAssetBytes {
		return StoredAsset{}, diagram.TooLarge("The file is too large")
	}
	mime := diagram.DetectMime(data)
	if mime == "" && d.SanitizeSVG != nil && (len(only) == 0 || slicesContains(only, diagram.MimeSVG)) {
		clean, isSVG, err := d.SanitizeSVG(data)
		if isSVG {
			if err != nil {
				return StoredAsset{}, diagram.UnsupportedMedia("The SVG contains content that is not allowed")
			}
			data, mime = clean, diagram.MimeSVG
		}
	}
	if mime == "" || len(only) > 0 && !slicesContains(only, mime) {
		return StoredAsset{}, diagram.UnsupportedMedia("Unsupported file type. Allowed: PNG, JPEG, WebP, SVG")
	}
	hash := diagram.HashBytes(data)
	put := func() error {
		return d.Blobs.Put(ctx, blobKey(hash), bytes.NewReader(data), int64(len(data)), mime)
	}
	existing, err := d.Sharing.Asset(ctx, hash)
	switch {
	case err == nil:
		// heal a blob that went missing
		obj, gerr := d.Blobs.Get(ctx, blobKey(hash))
		if gerr == nil {
			obj.Body.Close()
		} else if errors.Is(gerr, domain.ErrNotFound) {
			if err := put(); err != nil {
				return StoredAsset{}, err
			}
		} else {
			return StoredAsset{}, gerr
		}
		return StoredAsset{hash, existing.Mime, existing.Size, true}, nil
	case !errors.Is(err, domain.ErrNotFound):
		return StoredAsset{}, err
	}
	if err := put(); err != nil {
		return StoredAsset{}, err
	}
	fresh, err := d.Sharing.InsertAsset(ctx, diagram.Asset{Hash: hash, Mime: mime, Size: len(data), UploadedBy: actor, CreatedAt: d.clock()})
	return StoredAsset{hash, mime, len(data), !fresh}, err
}

func slicesContains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// AssetMeta describes a stored asset (not found for an unknown or
// malformed hash).
func (d *Diagrams) AssetMeta(ctx context.Context, hash string) (diagram.Asset, error) {
	if !diagram.ValidHash(hash) {
		return diagram.Asset{}, diagram.NotFound("Asset")
	}
	a, err := d.Sharing.Asset(ctx, hash)
	if errors.Is(err, domain.ErrNotFound) {
		return a, diagram.NotFound("Asset")
	}
	return a, err
}

// AssetBlob opens an asset's bytes; the caller closes the body.
func (d *Diagrams) AssetBlob(ctx context.Context, hash string) (Object, error) {
	if !diagram.ValidHash(hash) {
		return Object{}, diagram.NotFound("Asset")
	}
	obj, err := d.Blobs.Get(ctx, blobKey(hash))
	if errors.Is(err, domain.ErrNotFound) {
		return obj, diagram.NotFound("Asset")
	}
	return obj, err
}

// SetThumbnail stores a file's preview (PNG or WebP, at most 200 KB) and
// returns its hash. It does not change the file's version. Editors only.
func (d *Diagrams) SetThumbnail(ctx context.Context, actor int, fileID string, data []byte) (string, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.SetThumbnail")
	defer span.End()
	if _, _, err := d.fileAccess(ctx, actor, fileID, diagram.RoleEditor); err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", diagram.UnsupportedMedia("Send the image bytes with Content-Type: image/png or image/webp")
	}
	if len(data) > MaxThumbnailBytes {
		return "", diagram.TooLarge("The thumbnail may be at most 200 KB")
	}
	a, err := d.StoreAsset(ctx, actor, data, diagram.MimePNG, diagram.MimeWebP)
	if err != nil {
		return "", err
	}
	return a.Hash, d.Sharing.SetThumbnail(ctx, fileID, a.Hash)
}

// Thumbnail finds a file's preview for a caller who can read the file (a
// member, or a share token).
func (d *Diagrams) Thumbnail(ctx context.Context, actor int, token, fileID string) (diagram.Asset, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.Thumbnail")
	defer span.End()
	_, f, err := d.fileAccessVia(ctx, actor, token, fileID, diagram.RoleViewer)
	if err != nil {
		return diagram.Asset{}, err
	}
	if f.ThumbnailHash == nil {
		return diagram.Asset{}, diagram.NotFound("Thumbnail")
	}
	a, err := d.Sharing.Asset(ctx, *f.ThumbnailHash)
	if errors.Is(err, domain.ErrNotFound) {
		return diagram.Asset{Hash: *f.ThumbnailHash, Mime: diagram.MimePNG}, nil
	}
	return a, err
}

// Library

// readLibrary is the project's library; a project that never saved one has
// revision 0 and an empty document.
func (d *Diagrams) readLibrary(ctx context.Context, projectID string) (diagram.Library, error) {
	l, err := d.Sharing.Library(ctx, projectID)
	if l.Rev == 0 || len(l.Doc) == 0 {
		l.Doc = diagram.EmptyLibrary
	}
	return l, err
}

// GetLibrary returns the project library for a member or a project-wide
// share link.
func (d *Diagrams) GetLibrary(ctx context.Context, actor int, token, projectID string) (diagram.Library, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.GetLibrary")
	defer span.End()
	if _, err := d.projectAccessVia(ctx, actor, token, projectID, diagram.RoleViewer); err != nil {
		return diagram.Library{}, err
	}
	return d.readLibrary(ctx, projectID)
}

// SaveLibrary replaces the library if baseRev is the current revision; a
// stale one fails with a *diagram.LibraryConflict carrying the current
// library. Editors only.
func (d *Diagrams) SaveLibrary(ctx context.Context, actor int, projectID string, baseRev int, doc json.RawMessage) (diagram.Library, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.SaveLibrary")
	defer span.End()
	if baseRev < 0 {
		return diagram.Library{}, diagram.Validation("baseRev: must be an integer >= 0")
	}
	if err := diagram.CheckLibrary(doc); err != nil {
		return diagram.Library{}, err
	}
	if len(doc) > diagram.MaxLibraryBytes {
		return diagram.Library{}, diagram.TooLarge("The library is larger than 2 MB")
	}
	if _, err := d.projectAccess(ctx, actor, projectID, diagram.RoleEditor); err != nil {
		return diagram.Library{}, err
	}
	cur, err := d.readLibrary(ctx, projectID)
	if err != nil {
		return diagram.Library{}, err
	}
	if cur.Rev != baseRev {
		return diagram.Library{}, &diagram.LibraryConflict{Current: cur}
	}
	ok, err := d.Sharing.SaveLibrary(ctx, projectID, baseRev, doc, actor, d.clock())
	if err != nil {
		return diagram.Library{}, err
	}
	if !ok {
		cur, err := d.readLibrary(ctx, projectID)
		if err != nil {
			return diagram.Library{}, err
		}
		return diagram.Library{}, &diagram.LibraryConflict{Current: cur}
	}
	if d.Events != nil {
		d.emit(ctx, DiagramEvent{Kind: EventLibrary, ProjectID: projectID, Rev: baseRev + 1, By: d.actorOf(ctx, writer{user: actor})})
	}
	return diagram.Library{Rev: baseRev + 1, Doc: doc}, nil
}

// Private stickies

// GetPrivate returns the caller's own private stickies of a file: nobody
// else's, whatever their role. Viewers and up.
func (d *Diagrams) GetPrivate(ctx context.Context, actor int, fileID string) (diagram.Private, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.GetPrivate")
	defer span.End()
	if _, _, err := d.fileAccess(ctx, actor, fileID, diagram.RoleViewer); err != nil {
		return diagram.Private{}, err
	}
	p, err := d.Sharing.Private(ctx, fileID, actor)
	if errors.Is(err, domain.ErrNotFound) {
		return diagram.Private{Data: diagram.EmptyPrivate}, nil
	}
	return p, err
}

// PutPrivate replaces the caller's private stickies of a file; every sticky
// is stamped private and written by the caller. An empty set clears them.
// Editors only.
func (d *Diagrams) PutPrivate(ctx context.Context, actor int, fileID string, raw json.RawMessage) (diagram.Private, error) {
	ctx, span := tracer.Start(ctx, "Diagrams.PutPrivate")
	defer span.End()
	if _, _, err := d.fileAccess(ctx, actor, fileID, diagram.RoleEditor); err != nil {
		return diagram.Private{}, err
	}
	data, err := diagram.PreparePrivate(raw, strconv.Itoa(actor))
	if err != nil {
		return diagram.Private{}, err
	}
	now := d.clock()
	var parts struct{ Nodes, Edges []json.RawMessage }
	_ = json.Unmarshal(data, &parts)
	if len(parts.Nodes) == 0 && len(parts.Edges) == 0 {
		return diagram.Private{Data: diagram.EmptyPrivate, UpdatedAt: now}, d.Sharing.DeletePrivate(ctx, fileID, actor)
	}
	return diagram.Private{Data: data, UpdatedAt: now}, d.Sharing.PutPrivate(ctx, fileID, actor, data, now)
}

// scrubPrivate takes the private stickies out of a document that is about
// to be stored (see diagram.ScrubPrivate) and returns them by owner. A
// sticky belongs to its author when that is a known account, else to the
// writer (the project's owner for a share link). It stores nothing: the
// caller files them with filePrivate once the document is valid.
func (d *Diagrams) scrubPrivate(ctx context.Context, data json.RawMessage, w writer) (json.RawMessage, map[int]diagram.PrivateSet) {
	known := map[string]bool{}
	isUser := func(id string) bool {
		if ok, seen := known[id]; seen {
			return ok
		}
		// only the writer's own stickies are filed under their author: a
		// writer naming another account must not reach into that account's
		// private store, so those go to the fallback owner like unknown ones
		n, err := strconv.Atoi(id)
		ok := err == nil && n > 0 && strconv.Itoa(n) == id && n == w.user
		known[id] = ok
		return ok
	}
	clean, by, removed := diagram.ScrubPrivate(data, isUser, strconv.Itoa(w.fallbackOwner()))
	if removed == 0 {
		return data, nil
	}
	out := make(map[int]diagram.PrivateSet, len(by))
	for id, set := range by {
		n, _ := strconv.Atoi(id) // owners are accounts: isUser or the fallback
		out[n] = set
	}
	return clean, out
}

// filePrivate adds scrubbed stickies to their owners' own stores. Without a
// sharing repository there is no store: they are dropped, never kept in the
// shared document.
func (d *Diagrams) filePrivate(ctx context.Context, fileID string, by map[int]diagram.PrivateSet, now time.Time) error {
	if d.Sharing == nil {
		return nil
	}
	owners := make([]int, 0, len(by))
	for id := range by {
		owners = append(owners, id)
	}
	slices.Sort(owners)
	for _, owner := range owners {
		cur, err := d.Sharing.Private(ctx, fileID, owner)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			return err
		}
		if err := d.Sharing.PutPrivate(ctx, fileID, owner, diagram.MergePrivate(cur.Data, by[owner]), now); err != nil {
			return err
		}
	}
	return nil
}
