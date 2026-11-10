package http

import (
	"context"
	"io"
	"time"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/application"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/category"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/clienterror"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/comment"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/feedback"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/mfa"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/post"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/publication"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/readinglist"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/relation"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/report"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/revision"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/sanction"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/series"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/social"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/user"
)

// zctl switches every generated zero-value fake between succeeding (with
// empty results) and failing with err.
type zctl struct{ err error }

type zPosts struct{ c *zctl }

func (f *zPosts) List(ctx context.Context, _ post.Filter) (r0 post.Page, r1 error) {
	r1 = f.c.err
	return
}

func (f *zPosts) ListByIDs(ctx context.Context, ids []int) (r0 []post.Post, r1 error) {
	r1 = f.c.err
	return
}

func (f *zPosts) Get(ctx context.Context, id int, viewerID int) (r0 post.Post, r1 error) {
	r1 = f.c.err
	return
}

func (f *zPosts) GetBySlug(ctx context.Context, slug string, viewerID int) (r0 post.Post, r1 error) {
	r1 = f.c.err
	return
}

func (f *zPosts) Create(ctx context.Context, d post.Draft, authorID int, author string) (r0 post.Post, r1 error) {
	r1 = f.c.err
	return
}

func (f *zPosts) Update(ctx context.Context, id int, d post.Draft) (r0 post.Post, r1 error) {
	r1 = f.c.err
	return
}

func (f *zPosts) Delete(ctx context.Context, id int) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zPosts) Publish(ctx context.Context, id int, now time.Time) (r0 bool, r1 error) {
	r1 = f.c.err
	return
}

func (f *zPosts) DueScheduled(ctx context.Context, now time.Time) (r0 []int, r1 error) {
	r1 = f.c.err
	return
}

func (f *zPosts) Like(ctx context.Context, id int, userID int) (r0 post.Post, r1 bool, r2 error) {
	r2 = f.c.err
	return
}

func (f *zPosts) Clap(ctx context.Context, id int, userID int, n int) (r0 post.Post, r1 bool, r2 error) {
	r2 = f.c.err
	return
}

func (f *zPosts) Unlike(ctx context.Context, id int, userID int) (r0 post.Post, r1 error) {
	r1 = f.c.err
	return
}

func (f *zPosts) AddHighlight(ctx context.Context, h post.Highlight) (r0 post.Highlight, r1 error) {
	r1 = f.c.err
	return
}

func (f *zPosts) GetHighlight(ctx context.Context, id int) (r0 post.Highlight, r1 error) {
	r1 = f.c.err
	return
}

func (f *zPosts) DeleteHighlight(ctx context.Context, id int) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zPosts) Highlights(ctx context.Context, postID int, viewerID int, top int) (r0 post.Highlights, r1 error) {
	r1 = f.c.err
	return
}

func (f *zPosts) Read(ctx context.Context, postID int, userID int, reader string) (r0 bool, r1 error) {
	r1 = f.c.err
	return
}

func (f *zPosts) DailyReads(ctx context.Context, postID int, since time.Time) (r0 map[string]int, r1 error) {
	r1 = f.c.err
	return
}

func (f *zPosts) AuthorStats(ctx context.Context, userID int, p domain.Paging) (r0 []post.AuthorStat, r1 int, r2 error) {
	r2 = f.c.err
	return
}

func (f *zPosts) Tags(ctx context.Context) (r0 []post.TagCount, r1 error) {
	r1 = f.c.err
	return
}

func (f *zPosts) Labels(ctx context.Context) (r0 []post.Label, r1 error) {
	r1 = f.c.err
	return
}

func (f *zPosts) CreateLabel(ctx context.Context, l post.Label) (r0 post.Label, r1 error) {
	r1 = f.c.err
	return
}

func (f *zPosts) DeleteLabel(ctx context.Context, id int) (r0 error) {
	r0 = f.c.err
	return
}

type zComments struct{ c *zctl }

func (f *zComments) ListByPost(ctx context.Context, postID int, viewerID int, p domain.Paging) (r0 comment.Page, r1 error) {
	r1 = f.c.err
	return
}

func (f *zComments) ListAll(ctx context.Context, p domain.Paging) (r0 comment.Page, r1 error) {
	r1 = f.c.err
	return
}

func (f *zComments) Get(ctx context.Context, id int) (r0 comment.Comment, r1 error) {
	r1 = f.c.err
	return
}

func (f *zComments) Add(ctx context.Context, c comment.Comment) (r0 comment.Comment, r1 error) {
	r1 = f.c.err
	return
}

func (f *zComments) Edit(ctx context.Context, id int, text string) (r0 comment.Comment, r1 error) {
	r1 = f.c.err
	return
}

func (f *zComments) Delete(ctx context.Context, id int) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zComments) Like(ctx context.Context, id int, userID int) (r0 comment.Comment, r1 bool, r2 error) {
	r2 = f.c.err
	return
}

func (f *zComments) Unlike(ctx context.Context, id int, userID int) (r0 comment.Comment, r1 error) {
	r1 = f.c.err
	return
}

type zCats struct{ c *zctl }

func (f *zCats) List(ctx context.Context) (r0 []category.Category, r1 error) {
	r1 = f.c.err
	return
}

func (f *zCats) Exists(ctx context.Context, id int) (r0 bool, r1 error) {
	r1 = f.c.err
	return
}

func (f *zCats) Create(ctx context.Context, name string) (r0 category.Category, r1 error) {
	r1 = f.c.err
	return
}

func (f *zCats) Delete(ctx context.Context, id int) (r0 error) {
	r0 = f.c.err
	return
}

type zUsers struct{ c *zctl }

func (f *zUsers) Create(ctx context.Context, username string, email string) (r0 user.User, r1 error) {
	r1 = f.c.err
	return
}

func (f *zUsers) Ensure(ctx context.Context, username string, email string) (r0 user.User, r1 error) {
	r1 = f.c.err
	return
}

func (f *zUsers) MarkEmailVerified(ctx context.Context, id int) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zUsers) Delete(ctx context.Context, id int) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zUsers) GetByID(ctx context.Context, id int) (r0 user.User, r1 error) {
	r1 = f.c.err
	return
}

func (f *zUsers) GetByUsername(ctx context.Context, username string) (r0 user.User, r1 error) {
	r1 = f.c.err
	return
}

func (f *zUsers) GetByLogin(ctx context.Context, login string) (r0 user.User, r1 error) {
	r1 = f.c.err
	return
}

func (f *zUsers) List(ctx context.Context, query string, p domain.Paging) (r0 user.Page, r1 error) {
	r1 = f.c.err
	return
}

func (f *zUsers) Profile(ctx context.Context, username string, viewerID int) (r0 user.Profile, r1 error) {
	r1 = f.c.err
	return
}

func (f *zUsers) UpdateProfile(ctx context.Context, id int, u user.ProfileUpdate) (r0 user.User, r1 error) {
	r1 = f.c.err
	return
}

func (f *zUsers) Follow(ctx context.Context, followerID int, followeeID int) (r0 bool, r1 error) {
	r1 = f.c.err
	return
}

func (f *zUsers) Unfollow(ctx context.Context, followerID int, followeeID int) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zUsers) Followers(ctx context.Context, id int, p domain.Paging) (r0 user.SummaryPage, r1 error) {
	r1 = f.c.err
	return
}

func (f *zUsers) Following(ctx context.Context, id int, p domain.Paging) (r0 user.SummaryPage, r1 error) {
	r1 = f.c.err
	return
}

func (f *zUsers) FollowerIDs(ctx context.Context, id int) (r0 []int, r1 error) {
	r1 = f.c.err
	return
}

func (f *zUsers) SetPinned(ctx context.Context, id int, postID int) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zUsers) Subscribe(ctx context.Context, subscriberID int, authorID int) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zUsers) Unsubscribe(ctx context.Context, subscriberID int, authorID int) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zUsers) Subscribers(ctx context.Context, authorID int) (r0 []user.Subscriber, r1 error) {
	r1 = f.c.err
	return
}

func (f *zUsers) MarkDeleted(ctx context.Context, id int, at time.Time) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zUsers) Restore(ctx context.Context, id int) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zUsers) DuePurges(ctx context.Context, cutoff time.Time) (r0 []int, r1 error) {
	r1 = f.c.err
	return
}

func (f *zUsers) Purge(ctx context.Context, id int, cutoff time.Time) (r0 bool, r1 error) {
	r1 = f.c.err
	return
}

type zSocial struct{ c *zctl }

func (f *zSocial) Bookmark(ctx context.Context, userID int, postID int) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zSocial) Unbookmark(ctx context.Context, userID int, postID int) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zSocial) FollowTag(ctx context.Context, userID int, tag string) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zSocial) UnfollowTag(ctx context.Context, userID int, tag string) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zSocial) FollowedTags(ctx context.Context, userID int) (r0 []string, r1 error) {
	r1 = f.c.err
	return
}

func (f *zSocial) Notify(ctx context.Context, n social.Notification, recipients []int) (r0 []social.Notification, r1 error) {
	r1 = f.c.err
	return
}

func (f *zSocial) Notifications(ctx context.Context, userID int, p domain.Paging) (r0 social.NotificationPage, r1 error) {
	r1 = f.c.err
	return
}

func (f *zSocial) Hide(ctx context.Context, userID int, k social.HideKind, target string) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zSocial) Unhide(ctx context.Context, userID int, k social.HideKind, target string) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zSocial) Hidden(ctx context.Context, userID int, p domain.Paging) (r0 social.HidePage, r1 error) {
	r1 = f.c.err
	return
}

func (f *zSocial) CountHidden(ctx context.Context, userID int) (r0 int, r1 error) {
	r1 = f.c.err
	return
}

func (f *zSocial) DiagramNotifications(ctx context.Context, userID int, unreadOnly bool, limit int) (r0 []social.Notification, r1 int, r2 error) {
	r2 = f.c.err
	return
}

func (f *zSocial) MarkDiagramRead(ctx context.Context, userID int, ids []int64, all bool) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zSocial) MarkRead(ctx context.Context, userID int, ids []int64) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zSocial) MarkAllRead(ctx context.Context, userID int) (r0 error) {
	r0 = f.c.err
	return
}

type zReports struct{ c *zctl }

func (f *zReports) Create(ctx context.Context, r report.Report) (r0 report.Report, r1 error) {
	r1 = f.c.err
	return
}

func (f *zReports) Get(ctx context.Context, id int) (r0 report.Report, r1 error) {
	r1 = f.c.err
	return
}

func (f *zReports) List(ctx context.Context, status report.ReportStatus, p domain.Paging) (r0 report.Page, r1 error) {
	r1 = f.c.err
	return
}

func (f *zReports) Decide(ctx context.Context, id int, s report.ReportStatus, moderatorID int) (r0 report.Report, r1 error) {
	r1 = f.c.err
	return
}

type zFeedback struct{ c *zctl }

func (f *zFeedback) Create(ctx context.Context, _ feedback.Feedback) (r0 feedback.Feedback, r1 error) {
	r1 = f.c.err
	return
}

func (f *zFeedback) Get(ctx context.Context, id int) (r0 feedback.Feedback, r1 error) {
	r1 = f.c.err
	return
}

func (f *zFeedback) List(ctx context.Context, _ feedback.Filter) (r0 feedback.Page, r1 error) {
	r1 = f.c.err
	return
}

func (f *zFeedback) SetStatus(ctx context.Context, id int, s feedback.Status, moderatorID int) (r0 feedback.Feedback, r1 error) {
	r1 = f.c.err
	return
}

type zClientErrs struct{ c *zctl }

func (f *zClientErrs) Record(ctx context.Context, e clienterror.ClientError, maxRows int) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zClientErrs) List(ctx context.Context, _ clienterror.Filter) (r0 clienterror.Page, r1 error) {
	r1 = f.c.err
	return
}

func (f *zClientErrs) SetStatus(ctx context.Context, id int, s clienterror.Status) (r0 clienterror.ClientError, r1 error) {
	r1 = f.c.err
	return
}

func (f *zClientErrs) PurgeBefore(ctx context.Context, t time.Time) (r0 int64, r1 error) {
	r1 = f.c.err
	return
}

type zPubs struct{ c *zctl }

func (f *zPubs) Create(ctx context.Context, in publication.PublicationInput, ownerID int) (r0 publication.Publication, r1 error) {
	r1 = f.c.err
	return
}

func (f *zPubs) GetBySlug(ctx context.Context, slug string, viewerID int) (r0 publication.Publication, r1 error) {
	r1 = f.c.err
	return
}

func (f *zPubs) Update(ctx context.Context, id int, in publication.PublicationInput) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zPubs) Delete(ctx context.Context, id int) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zPubs) ListForUser(ctx context.Context, userID int) (r0 []publication.Publication, r1 error) {
	r1 = f.c.err
	return
}

func (f *zPubs) Role(ctx context.Context, publicationID int, userID int) (r0 publication.Role, r1 error) {
	r1 = f.c.err
	return
}

func (f *zPubs) Members(ctx context.Context, publicationID int) (r0 []publication.Member, r1 error) {
	r1 = f.c.err
	return
}

func (f *zPubs) SetMember(ctx context.Context, publicationID int, userID int, r publication.Role) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zPubs) RemoveMember(ctx context.Context, publicationID int, userID int) (r0 error) {
	r0 = f.c.err
	return
}

type zRel struct{ c *zctl }

func (f *zRel) Add(ctx context.Context, userID int, targetID int, k relation.Kind) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zRel) Remove(ctx context.Context, userID int, targetID int, k relation.Kind) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zRel) List(ctx context.Context, userID int, k relation.Kind, p domain.Paging) (r0 user.SummaryPage, r1 error) {
	r1 = f.c.err
	return
}

func (f *zRel) Blocked(ctx context.Context, a int, b int) (r0 bool, r1 error) {
	r1 = f.c.err
	return
}

func (f *zRel) Silenced(ctx context.Context, actorID int, recipients []int) (r0 []int, r1 error) {
	r1 = f.c.err
	return
}

type zRev struct{ c *zctl }

func (f *zRev) Add(ctx context.Context, postID int, editorID int, c revision.Content) (r0 revision.Revision, r1 error) {
	r1 = f.c.err
	return
}

func (f *zRev) List(ctx context.Context, postID int) (r0 []revision.Revision, r1 error) {
	r1 = f.c.err
	return
}

func (f *zRev) Get(ctx context.Context, postID int, version int) (r0 revision.Revision, r1 error) {
	r1 = f.c.err
	return
}

type zSeries struct{ c *zctl }

func (f *zSeries) Create(ctx context.Context, userID int, slug string, in series.SeriesInput) (r0 series.Series, r1 error) {
	r1 = f.c.err
	return
}

func (f *zSeries) Update(ctx context.Context, id int, in series.SeriesInput) (r0 series.Series, r1 error) {
	r1 = f.c.err
	return
}

func (f *zSeries) Delete(ctx context.Context, id int) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zSeries) GetBySlug(ctx context.Context, slug string) (r0 series.Series, r1 error) {
	r1 = f.c.err
	return
}

func (f *zSeries) ByUser(ctx context.Context, userID int) (r0 []series.Series, r1 error) {
	r1 = f.c.err
	return
}

func (f *zSeries) Parts(ctx context.Context, id int, all bool) (r0 []series.Part, r1 error) {
	r1 = f.c.err
	return
}

func (f *zSeries) SetPosts(ctx context.Context, id int, ownerID int, postIDs []int) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zSeries) OfPost(ctx context.Context, postID int) (r0 series.Series, r1 error) {
	r1 = f.c.err
	return
}

type zLists struct{ c *zctl }

func (f *zLists) Create(ctx context.Context, userID int, slug string, in readinglist.ListInput) (r0 readinglist.List, r1 error) {
	r1 = f.c.err
	return
}

func (f *zLists) Update(ctx context.Context, id int, in readinglist.ListInput) (r0 readinglist.List, r1 error) {
	r1 = f.c.err
	return
}

func (f *zLists) Delete(ctx context.Context, id int) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zLists) GetBySlug(ctx context.Context, slug string) (r0 readinglist.List, r1 error) {
	r1 = f.c.err
	return
}

func (f *zLists) ByUser(ctx context.Context, userID int, private bool, postID int, p domain.Paging) (r0 readinglist.Page, r1 error) {
	r1 = f.c.err
	return
}

func (f *zLists) Count(ctx context.Context, userID int) (r0 int, r1 error) {
	r1 = f.c.err
	return
}

func (f *zLists) Add(ctx context.Context, id int, postID int) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zLists) Remove(ctx context.Context, id int, postID int) (r0 error) {
	r0 = f.c.err
	return
}

type zMFA struct{ c *zctl }

func (f *zMFA) Get(ctx context.Context, userID int) (r0 mfa.State, r1 error) {
	r1 = f.c.err
	return
}

func (f *zMFA) SavePending(ctx context.Context, userID int, secret string) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zMFA) Enable(ctx context.Context, userID int, step int64, codeHashes []string) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zMFA) Disable(ctx context.Context, userID int) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zMFA) UseStep(ctx context.Context, userID int, step int64) (r0 bool, r1 error) {
	r1 = f.c.err
	return
}

func (f *zMFA) UseBackupCode(ctx context.Context, userID int, hash string) (r0 bool, r1 error) {
	r1 = f.c.err
	return
}

func (f *zMFA) ReplaceBackupCodes(ctx context.Context, userID int, hashes []string) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zMFA) BackupCodesLeft(ctx context.Context, userID int) (r0 int, r1 error) {
	r1 = f.c.err
	return
}

type zSanc struct{ c *zctl }

func (f *zSanc) Save(ctx context.Context, s sanction.Sanction) (r0 sanction.Sanction, r1 error) {
	r1 = f.c.err
	return
}

func (f *zSanc) Get(ctx context.Context, userID int) (r0 sanction.Sanction, r1 error) {
	r1 = f.c.err
	return
}

func (f *zSanc) Delete(ctx context.Context, userID int) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zSanc) Expired(ctx context.Context, now time.Time) (r0 []int, r1 error) {
	r1 = f.c.err
	return
}

func (f *zSanc) List(ctx context.Context, p domain.Paging) (r0 []sanction.Sanction, r1 int, r2 error) {
	r2 = f.c.err
	return
}

type zStats struct{ c *zctl }

func (f *zStats) Stats(ctx context.Context) (r0 application.Stats, r1 error) {
	r1 = f.c.err
	return
}

type zDisc struct{ c *zctl }

func (f *zDisc) RelatedPosts(ctx context.Context, postID int, limit int) (r0 []int, r1 error) {
	r1 = f.c.err
	return
}

func (f *zDisc) SuggestUsers(ctx context.Context, userID int, limit int) (r0 []application.SuggestedUser, r1 error) {
	r1 = f.c.err
	return
}

func (f *zDisc) UserIDs(ctx context.Context, usernames []string) (r0 []int, r1 error) {
	r1 = f.c.err
	return
}

func (f *zDisc) ForYou(ctx context.Context, userID int, limit int, offset int) (r0 []int, r1 int, r2 error) {
	r2 = f.c.err
	return
}

type zAuthz struct{ c *zctl }

func (f *zAuthz) Can(ctx context.Context, resource string, action string, id int, ownerID int) (r0 error) {
	r0 = f.c.err
	return
}

type zAudit struct{ c *zctl }

func (f *zAudit) Record(ctx context.Context, actorID int, action string, target string, meta map[string]any) {
}

type zJobs struct{ c *zctl }

func (f *zJobs) PublishPostAt(ctx context.Context, postID int, at time.Time) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zJobs) FanoutNewPost(ctx context.Context, postID int) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zJobs) PurgeAccountAt(ctx context.Context, userID int, at time.Time) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zJobs) SendEmail(ctx context.Context, m application.Email) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zJobs) SendEmailOnce(ctx context.Context, id string, m application.Email) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zJobs) Thumbnail(ctx context.Context, key string) (r0 error) {
	r0 = f.c.err
	return
}

type zEvents struct{ c *zctl }

func (f *zEvents) Publish(ctx context.Context, channel string, v any) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zEvents) Subscribe(ctx context.Context, channel string) (r0 <-chan []byte, r1 error) {
	r1 = f.c.err
	return
}

type zViews struct{ c *zctl }

func (f *zViews) Hit(ctx context.Context, postID int, viewer string) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zViews) Count(ctx context.Context, postID int) (r0 int64) {
	return
}

func (f *zViews) Counts(ctx context.Context, postIDs []int) (r0 map[int]int64) {
	return
}

func (f *zViews) Trending(ctx context.Context, days int, limit int) (r0 []int, r1 error) {
	r1 = f.c.err
	return
}

func (f *zViews) Daily(ctx context.Context, postID int, days []time.Time) (r0 []int64, r1 error) {
	r1 = f.c.err
	return
}

func (f *zViews) DailyTotals(ctx context.Context, days []time.Time) (r0 []int64, r1 error) {
	r1 = f.c.err
	return
}

type zImporter struct{ c *zctl }

func (f *zImporter) Fetch(ctx context.Context, url string) (r0 application.Imported, r1 error) {
	r1 = f.c.err
	return
}

type zDiagMod struct{ c *zctl }

func (f *zDiagMod) ReportTarget(ctx context.Context, reporter int, fileID string) (r0 string, r1 int, r2 error) {
	r2 = f.c.err
	return
}

func (f *zDiagMod) AdminDeleteFile(ctx context.Context, actor int, fileID string) (r0 error) {
	r0 = f.c.err
	return
}

type zIdent struct{ c *zctl }

func (f *zIdent) CreateAccount(ctx context.Context, userID int, email string, password string) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zIdent) EnsureSuperAdmin(ctx context.Context, userID int, email string, password string) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zIdent) Login(ctx context.Context, email string, password string, m application.Meta) (r0 application.Credentials, r1 error) {
	r1 = f.c.err
	return
}

func (f *zIdent) Refresh(ctx context.Context, token string) (r0 application.Credentials, r1 error) {
	r1 = f.c.err
	return
}

func (f *zIdent) Logout(ctx context.Context, token string, m application.Meta) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zIdent) LogoutAll(ctx context.Context, userID int) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zIdent) Authenticate(ctx context.Context, token string) (r0 int, r1 error) {
	r1 = f.c.err
	return
}

func (f *zIdent) VerifyPassword(ctx context.Context, email string, password string) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zIdent) ChangePassword(ctx context.Context, userID int, oldPassword string, newPassword string) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zIdent) SetPassword(ctx context.Context, userID int, password string) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zIdent) Roles(ctx context.Context, userID int) (r0 []string, r1 error) {
	r1 = f.c.err
	return
}

func (f *zIdent) StartSession(ctx context.Context, userID int, m application.Meta) (r0 application.Credentials, r1 error) {
	r1 = f.c.err
	return
}

func (f *zIdent) Sessions(ctx context.Context, userID int) (r0 []application.SessionInfo, r1 error) {
	r1 = f.c.err
	return
}

func (f *zIdent) RevokeSession(ctx context.Context, userID int, sessionID string) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zIdent) SessionID(ctx context.Context, token string) (r0 string, r1 error) {
	r1 = f.c.err
	return
}

func (f *zIdent) SetBlocked(ctx context.Context, actorID int, userID int, blocked bool) (r0 error) {
	r0 = f.c.err
	return
}

type zTokens struct{ c *zctl }

func (f *zTokens) Issue(ctx context.Context, userID int, ttl time.Duration) (r0 string, r1 error) {
	r1 = f.c.err
	return
}

func (f *zTokens) Consume(ctx context.Context, token string) (r0 int, r1 error) {
	r1 = f.c.err
	return
}

type zChal struct{ c *zctl }

func (f *zChal) Issue(ctx context.Context, userID int, ttl time.Duration) (r0 string, r1 error) {
	r1 = f.c.err
	return
}

func (f *zChal) Attempt(ctx context.Context, token string) (r0 int, r1 error) {
	r1 = f.c.err
	return
}

func (f *zChal) Consume(ctx context.Context, token string) (r0 error) {
	r0 = f.c.err
	return
}

type zSecret struct{ c *zctl }

func (f *zSecret) Seal(plain string) (r0 string, r1 error) {
	r1 = f.c.err
	return
}

func (f *zSecret) Open(sealed string) (r0 string, r1 error) {
	r1 = f.c.err
	return
}

type zSocLinks struct{ c *zctl }

func (f *zSocLinks) UserBySubject(ctx context.Context, provider string, subject string) (r0 int, r1 error) {
	r1 = f.c.err
	return
}

func (f *zSocLinks) Link(ctx context.Context, provider string, subject string, userID int) (r0 error) {
	r0 = f.c.err
	return
}

type zAnaRepo struct{ c *zctl }

func (f *zAnaRepo) Insert(ctx context.Context, e application.AnalyticsEvent) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zAnaRepo) Report(ctx context.Context, since time.Time, days int) (r0 application.AnalyticsReport, r1 error) {
	r1 = f.c.err
	return
}

func (f *zAnaRepo) PurgeBefore(ctx context.Context, before time.Time) (r0 int64, r1 error) {
	r1 = f.c.err
	return
}

type zSiteRepo struct{ c *zctl }

func (f *zSiteRepo) Counts(ctx context.Context) (r0 application.SiteCounts, r1 error) {
	r1 = f.c.err
	return
}

func (f *zSiteRepo) PublicPosts(ctx context.Context) (r0 []application.PostRef, r1 error) {
	r1 = f.c.err
	return
}

func (f *zSiteRepo) PublicDiagrams(ctx context.Context) (r0 []application.DiagramRef, r1 error) {
	r1 = f.c.err
	return
}

func (f *zSiteRepo) PageViews(ctx context.Context, since time.Time) (r0 []application.DayCount, r1 error) {
	r1 = f.c.err
	return
}

type zStorage struct{ c *zctl }

func (f *zStorage) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) (r0 error) {
	r0 = f.c.err
	return
}

func (f *zStorage) Get(ctx context.Context, key string) (r0 application.Object, r1 error) {
	r1 = f.c.err
	return
}
