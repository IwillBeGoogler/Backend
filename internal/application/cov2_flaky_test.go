package application

// Generated fault-injection wrapper: fails the failAt-th repository call.

import (
	"context"
	"encoding/json"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/diagram/diagramtest"
)

type flaky struct {
	*diagramtest.Memory
	calls, failAt int
}

func (f *flaky) hit() error {
	f.calls++
	if f.calls == f.failAt {
		return errBoom
	}
	return nil
}

func (f *flaky) Comment(p0 context.Context, p1 string) (r0 diagram.Comment, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.Comment(p0, p1)
}

func (f *flaky) Comments(p0 context.Context, p1 string) (r0 []diagram.Comment, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.Comments(p0, p1)
}

func (f *flaky) InsertComment(p0 context.Context, p1 diagram.Comment) (err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.InsertComment(p0, p1)
}

func (f *flaky) UpdateComment(p0 context.Context, p1 diagram.Comment, p2 bool, p3 bool) (err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.UpdateComment(p0, p1, p2, p3)
}

func (f *flaky) DeleteComment(p0 context.Context, p1 string) (r0 []string, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.DeleteComment(p0, p1)
}

func (f *flaky) ReadAt(p0 context.Context, p1 string, p2 int) (r0 time.Time, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.ReadAt(p0, p1, p2)
}

func (f *flaky) MarkRead(p0 context.Context, p1 string, p2 int, p3 time.Time) (err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.MarkRead(p0, p1, p2, p3)
}

func (f *flaky) PublicListing(p0 context.Context, p1 string) (r0 diagram.Listing, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.PublicListing(p0, p1)
}

func (f *flaky) Clap(p0 context.Context, p1 string, p2 int, p3 int) (r0 int, r1 int, r2 bool, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.Clap(p0, p1, p2, p3)
}

func (f *flaky) Bookmark(p0 context.Context, p1 string, p2 int) (err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.Bookmark(p0, p1, p2)
}

func (f *flaky) Unbookmark(p0 context.Context, p1 string, p2 int) (err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.Unbookmark(p0, p1, p2)
}

func (f *flaky) Viewer(p0 context.Context, p1 string, p2 int) (r0 diagram.Viewer, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.Viewer(p0, p1, p2)
}

func (f *flaky) ListBookmarked(p0 context.Context, p1 int, p2 int, p3 int) (r0 []diagram.Listing, r1 int, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.ListBookmarked(p0, p1, p2, p3)
}

func (f *flaky) CreateToken(p0 context.Context, p1 diagram.AccessToken) (err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.CreateToken(p0, p1)
}

func (f *flaky) Tokens(p0 context.Context, p1 int) (r0 []diagram.AccessToken, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.Tokens(p0, p1)
}

func (f *flaky) TokenByHash(p0 context.Context, p1 string) (r0 diagram.AccessToken, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.TokenByHash(p0, p1)
}

func (f *flaky) RevokeToken(p0 context.Context, p1 int, p2 string, p3 time.Time) (err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.RevokeToken(p0, p1, p2, p3)
}

func (f *flaky) TouchToken(p0 context.Context, p1 string, p2 time.Time) (err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.TouchToken(p0, p1, p2)
}

func (f *flaky) CreateWebhook(p0 context.Context, p1 diagram.Webhook) (err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.CreateWebhook(p0, p1)
}

func (f *flaky) Webhooks(p0 context.Context, p1 string) (r0 []diagram.Webhook, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.Webhooks(p0, p1)
}

func (f *flaky) Webhook(p0 context.Context, p1 string, p2 string) (r0 diagram.Webhook, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.Webhook(p0, p1, p2)
}

func (f *flaky) WebhookByID(p0 context.Context, p1 string) (r0 diagram.Webhook, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.WebhookByID(p0, p1)
}

func (f *flaky) UpdateWebhook(p0 context.Context, p1 diagram.Webhook) (err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.UpdateWebhook(p0, p1)
}

func (f *flaky) DeleteWebhook(p0 context.Context, p1 string) (err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.DeleteWebhook(p0, p1)
}

func (f *flaky) ActiveWebhooks(p0 context.Context, p1 string) (r0 []diagram.Webhook, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.ActiveWebhooks(p0, p1)
}

func (f *flaky) CreateDelivery(p0 context.Context, p1 diagram.Delivery) (err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.CreateDelivery(p0, p1)
}

func (f *flaky) Delivery(p0 context.Context, p1 string) (r0 diagram.Delivery, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.Delivery(p0, p1)
}

func (f *flaky) UpdateDelivery(p0 context.Context, p1 diagram.Delivery) (err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.UpdateDelivery(p0, p1)
}

func (f *flaky) Deliveries(p0 context.Context, p1 string, p2 int) (r0 []diagram.Delivery, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.Deliveries(p0, p1, p2)
}

func (f *flaky) AddAudit(p0 context.Context, p1 diagram.MCPAudit) (err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.AddAudit(p0, p1)
}

func (f *flaky) Audit(p0 context.Context, p1 int, p2 int) (r0 []diagram.MCPAudit, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.Audit(p0, p1, p2)
}

func (f *flaky) CreateProject(p0 context.Context, p1 diagram.Project) (err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.CreateProject(p0, p1)
}

func (f *flaky) Project(p0 context.Context, p1 string) (r0 diagram.Project, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.Project(p0, p1)
}

func (f *flaky) ListProjects(p0 context.Context, p1 int, p2 diagram.ProjectQuery) (r0 []diagram.ProjectSummary, r1 int, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.ListProjects(p0, p1, p2)
}

func (f *flaky) Counts(p0 context.Context, p1 string) (r0 int, r1 int, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.Counts(p0, p1)
}

func (f *flaky) ProjectThumb(p0 context.Context, p1 string) (r0 string, r1 *string, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.ProjectThumb(p0, p1)
}

func (f *flaky) RenameProject(p0 context.Context, p1 string, p2 string, p3 time.Time) (err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.RenameProject(p0, p1, p2, p3)
}

func (f *flaky) TouchTree(p0 context.Context, p1 string, p2 int, p3 time.Time) (r0 int, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.TouchTree(p0, p1, p2, p3)
}

func (f *flaky) DeleteProject(p0 context.Context, p1 string) (err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.DeleteProject(p0, p1)
}

func (f *flaky) MemberRole(p0 context.Context, p1 string, p2 int) (r0 diagram.Role, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.MemberRole(p0, p1, p2)
}

func (f *flaky) FileRole(p0 context.Context, p1 string, p2 int) (r0 diagram.Role, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.FileRole(p0, p1, p2)
}

func (f *flaky) Tree(p0 context.Context, p1 string) (r0 []diagram.Folder, r1 []diagram.File, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.Tree(p0, p1)
}

func (f *flaky) Folder(p0 context.Context, p1 string) (r0 diagram.Folder, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.Folder(p0, p1)
}

func (f *flaky) CreateFolder(p0 context.Context, p1 diagram.Folder) (err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.CreateFolder(p0, p1)
}

func (f *flaky) UpdateFolder(p0 context.Context, p1 diagram.Folder) (err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.UpdateFolder(p0, p1)
}

func (f *flaky) NextPosition(p0 context.Context, p1 string, p2 *string) (r0 float64, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.NextPosition(p0, p1, p2)
}

func (f *flaky) DeleteFolders(p0 context.Context, p1 string, p2 []string) (r0 int, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.DeleteFolders(p0, p1, p2)
}

func (f *flaky) InsertTree(p0 context.Context, p1 []diagram.Folder, p2 []diagram.NewFile) (err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.InsertTree(p0, p1, p2)
}

func (f *flaky) File(p0 context.Context, p1 string) (r0 diagram.Doc, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.File(p0, p1)
}

func (f *flaky) InsertFile(p0 context.Context, p1 diagram.NewFile) (err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.InsertFile(p0, p1)
}

func (f *flaky) UpdateFileMeta(p0 context.Context, p1 diagram.File) (err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.UpdateFileMeta(p0, p1)
}

func (f *flaky) SaveFile(p0 context.Context, p1 diagram.Doc, p2 int, p3 diagram.Version) (r0 bool, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.SaveFile(p0, p1, p2, p3)
}

func (f *flaky) SaveFileOver(p0 context.Context, p1 diagram.Doc, p2 int, p3 diagram.Version) (r0 bool, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.SaveFileOver(p0, p1, p2, p3)
}

func (f *flaky) DeleteFile(p0 context.Context, p1 string) (err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.DeleteFile(p0, p1)
}

func (f *flaky) Versions(p0 context.Context, p1 string) (r0 []diagram.Version, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.Versions(p0, p1)
}

func (f *flaky) GetVersion(p0 context.Context, p1 string, p2 int) (r0 diagram.Version, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.GetVersion(p0, p1, p2)
}

func (f *flaky) VersionInfo(p0 context.Context, p1 string, p2 int) (r0 diagram.Version, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.VersionInfo(p0, p1, p2)
}

func (f *flaky) CountVersions(p0 context.Context, p1 string) (r0 int, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.CountVersions(p0, p1)
}

func (f *flaky) NameHead(p0 context.Context, p1 string, p2 string, p3 int, p4 time.Time) (r0 diagram.Version, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.NameHead(p0, p1, p2, p3, p4)
}

func (f *flaky) DeleteVersions(p0 context.Context, p1 string, p2 []int) (err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.DeleteVersions(p0, p1, p2)
}

func (f *flaky) Prefs(p0 context.Context, p1 int) (r0 json.RawMessage, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.Prefs(p0, p1)
}

func (f *flaky) SetPublication(p0 context.Context, p1 string, p2 diagram.Publication) (err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.SetPublication(p0, p1, p2)
}

func (f *flaky) ClearPublication(p0 context.Context, p1 string) (err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.ClearPublication(p0, p1)
}

func (f *flaky) Listing(p0 context.Context, p1 string) (r0 diagram.Listing, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.Listing(p0, p1)
}

func (f *flaky) PublicBySlug(p0 context.Context, p1 string) (r0 diagram.PublicDoc, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.PublicBySlug(p0, p1)
}

func (f *flaky) ListPublic(p0 context.Context, p1 diagram.PublicQuery) (r0 []diagram.Listing, r1 int, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.ListPublic(p0, p1)
}

func (f *flaky) ListOwned(p0 context.Context, p1 int, p2 int, p3 int) (r0 []diagram.Listing, r1 int, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.ListOwned(p0, p1, p2, p3)
}

func (f *flaky) ReferencedPublic(p0 context.Context, p1 []string) (r0 map[string]diagram.Listing, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.ReferencedPublic(p0, p1)
}

func (f *flaky) Stats(p0 context.Context, p1 time.Time) (r0 diagram.AdminStats, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.Stats(p0, p1)
}

func (f *flaky) Projects(p0 context.Context, p1 diagram.AdminProjectQuery) (r0 []diagram.AdminProject, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.Projects(p0, p1)
}

func (f *flaky) ProjectDetail(p0 context.Context, p1 string) (r0 diagram.AdminProjectDetail, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.ProjectDetail(p0, p1)
}

func (f *flaky) RevokeShareByID(p0 context.Context, p1 string, p2 time.Time) (r0 string, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.RevokeShareByID(p0, p1, p2)
}

func (f *flaky) Members(p0 context.Context, p1 string) (r0 []diagram.Member, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.Members(p0, p1)
}

func (f *flaky) SetMember(p0 context.Context, p1 string, p2 int, p3 diagram.Role, p4 time.Time) (err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.SetMember(p0, p1, p2, p3, p4)
}

func (f *flaky) UpdateMember(p0 context.Context, p1 string, p2 int, p3 diagram.Role) (r0 bool, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.UpdateMember(p0, p1, p2, p3)
}

func (f *flaky) RemoveMember(p0 context.Context, p1 string, p2 int) (r0 bool, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.RemoveMember(p0, p1, p2)
}

func (f *flaky) FileMembers(p0 context.Context, p1 string) (r0 []diagram.Member, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.FileMembers(p0, p1)
}

func (f *flaky) SetFileMember(p0 context.Context, p1 string, p2 int, p3 diagram.Role, p4 time.Time) (err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.SetFileMember(p0, p1, p2, p3, p4)
}

func (f *flaky) RemoveFileMember(p0 context.Context, p1 string, p2 int) (r0 bool, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.RemoveFileMember(p0, p1, p2)
}

func (f *flaky) CreateShare(p0 context.Context, p1 diagram.ShareLink) (err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.CreateShare(p0, p1)
}

func (f *flaky) Shares(p0 context.Context, p1 string) (r0 []diagram.ShareLink, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.Shares(p0, p1)
}

func (f *flaky) RevokeShare(p0 context.Context, p1 string, p2 string, p3 time.Time) (r0 bool, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.RevokeShare(p0, p1, p2, p3)
}

func (f *flaky) SharesExpiredBetween(p0 context.Context, p1 time.Time, p2 time.Time) (r0 []diagram.ShareLink, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.SharesExpiredBetween(p0, p1, p2)
}

func (f *flaky) ShareByHash(p0 context.Context, p1 string) (r0 diagram.ShareLink, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.ShareByHash(p0, p1)
}

func (f *flaky) Asset(p0 context.Context, p1 string) (r0 diagram.Asset, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.Asset(p0, p1)
}

func (f *flaky) InsertAsset(p0 context.Context, p1 diagram.Asset) (r0 bool, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.InsertAsset(p0, p1)
}

func (f *flaky) SetThumbnail(p0 context.Context, p1 string, p2 string) (err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.SetThumbnail(p0, p1, p2)
}

func (f *flaky) Library(p0 context.Context, p1 string) (r0 diagram.Library, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.Library(p0, p1)
}

func (f *flaky) SaveLibrary(p0 context.Context, p1 string, p2 int, p3 json.RawMessage, p4 int, p5 time.Time) (r0 bool, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.SaveLibrary(p0, p1, p2, p3, p4, p5)
}

func (f *flaky) Private(p0 context.Context, p1 string, p2 int) (r0 diagram.Private, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.Private(p0, p1, p2)
}

func (f *flaky) PutPrivate(p0 context.Context, p1 string, p2 int, p3 json.RawMessage, p4 time.Time) (err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.PutPrivate(p0, p1, p2, p3, p4)
}

func (f *flaky) DeletePrivate(p0 context.Context, p1 string, p2 int) (err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.DeletePrivate(p0, p1, p2)
}

func (f *flaky) YLoad(p0 context.Context, p1 string) (r0 diagram.YState, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.YLoad(p0, p1)
}

func (f *flaky) YAppend(p0 context.Context, p1 string, p2 []byte) (r0 int64, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.YAppend(p0, p1, p2)
}

func (f *flaky) YAppendMany(p0 context.Context, p1 string, p2 [][]byte) (r0 int64, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.YAppendMany(p0, p1, p2)
}

func (f *flaky) YCompact(p0 context.Context, p1 string, p2 []byte, p3 int64) (r0 bool, err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.YCompact(p0, p1, p2, p3)
}

func (f *flaky) YReset(p0 context.Context, p1 string) (err error) {
	if err = f.hit(); err != nil {
		return
	}
	return f.Memory.YReset(p0, p1)
}
