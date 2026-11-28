package application

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"time"

	"go.uber.org/zap"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/challenge"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/social"
	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/user"
)

// Challenges

func (b *Blog) CreateChallenge(ctx context.Context, actor user.User, in challenge.Input) (challenge.Challenge, error) {
	ctx, span := tracer.Start(ctx, "Blog.CreateChallenge")
	defer span.End()
	if err := b.Authz.Can(ctx, "post", "create", 0, 0); err != nil {
		return challenge.Challenge{}, err
	}
	if err := b.requireVerified(actor); err != nil {
		return challenge.Challenge{}, err
	}
	in, err := in.Normalize()
	if err != nil {
		return challenge.Challenge{}, err
	}
	now := b.now()
	n, err := b.Challenges.CountActive(ctx, actor.ID, now)
	if err != nil {
		return challenge.Challenge{}, err
	}
	if n >= challenge.MaxActive {
		return challenge.Challenge{}, challenge.ErrTooManyActive
	}
	return b.Challenges.Create(ctx, actor.ID, in, now)
}

// ownChallenge loads a challenge the actor may change: its owner, or roles
// with post.update. A private one stays hidden (not found) from others.
func (b *Blog) ownChallenge(ctx context.Context, id int) (challenge.Challenge, error) {
	c, err := b.Challenges.Get(ctx, id)
	if err != nil {
		return c, err
	}
	if err := b.Authz.Can(ctx, "post", "update", 0, c.UserID); err != nil {
		if c.Visibility == challenge.Private {
			return challenge.Challenge{}, domain.ErrNotFound
		}
		return c, err
	}
	return c, nil
}

// canSee reports whether the viewer may read the challenge.
func (b *Blog) canSee(ctx context.Context, c challenge.Challenge, viewerID int) bool {
	return c.Visibility == challenge.Public || viewerID != 0 &&
		(viewerID == c.UserID || b.Authz.Can(ctx, "post", "update", 0, c.UserID) == nil)
}

func (b *Blog) GetChallenge(ctx context.Context, id, viewerID int) (challenge.Detail, error) {
	ctx, span := tracer.Start(ctx, "Blog.GetChallenge")
	defer span.End()
	c, err := b.Challenges.Get(ctx, id)
	if err != nil {
		return challenge.Detail{}, err
	}
	if !b.canSee(ctx, c, viewerID) {
		return challenge.Detail{}, domain.ErrNotFound
	}
	days, err := b.Challenges.Days(ctx, id)
	if err != nil {
		return challenge.Detail{}, err
	}
	if days == nil {
		days = []challenge.Day{}
	}
	done := make([]string, len(days))
	for i, d := range days {
		done[i] = d.Date
	}
	stats := challenge.Compute(c.StartDate, c.Days, done, challenge.Today(c.Timezone, b.now()))
	d := challenge.Detail{Challenge: c, Stats: stats, Entries: days}
	today := challenge.Today(c.Timezone, b.now())
	if d.Followers, err = b.Challenges.CountFollowers(ctx, id); err != nil {
		return challenge.Detail{}, err
	}
	if d.CheersToday, err = b.Challenges.CheersOn(ctx, id, today); err != nil {
		return challenge.Detail{}, err
	}
	cert, err := b.Challenges.CertificateOf(ctx, id)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		return challenge.Detail{}, err
	}
	d.CertificateID = cert.ID
	if viewerID == 0 {
		return d, nil
	}
	if d.IsFollowing, err = b.Challenges.IsFollowing(ctx, id, viewerID); err != nil {
		return challenge.Detail{}, err
	}
	if d.CheeredToday, err = b.Challenges.Cheered(ctx, id, viewerID, today); err != nil {
		return challenge.Detail{}, err
	}
	if viewerID == c.UserID {
		d.Reminders, d.RemindAt = &c.Reminders, c.RemindAt
	}
	return d, nil
}

// UpdateChallenge changes title, description, visibility and reminders.
func (b *Blog) UpdateChallenge(ctx context.Context, id int, in challenge.Input) (challenge.Challenge, error) {
	ctx, span := tracer.Start(ctx, "Blog.UpdateChallenge")
	defer span.End()
	c, err := b.ownChallenge(ctx, id)
	if err != nil {
		return challenge.Challenge{}, err
	}
	in.Days, in.Timezone = c.Days, c.Timezone // immutable
	if in, err = in.Normalize(); err != nil {
		return challenge.Challenge{}, err
	}
	return b.Challenges.Update(ctx, id, in)
}

// DeleteChallenge removes the challenge; its posts stay.
func (b *Blog) DeleteChallenge(ctx context.Context, id int) error {
	ctx, span := tracer.Start(ctx, "Blog.DeleteChallenge")
	defer span.End()
	if _, err := b.ownChallenge(ctx, id); err != nil {
		return err
	}
	return b.Challenges.Delete(ctx, id)
}

// LinkChallengePost adds one of the owner's posts (drafts too) to a running
// challenge.
func (b *Blog) LinkChallengePost(ctx context.Context, actor user.User, id, postID int) error {
	ctx, span := tracer.Start(ctx, "Blog.LinkChallengePost")
	defer span.End()
	c, err := b.ownChallenge(ctx, id)
	if err != nil {
		return err
	}
	if challenge.Compute(c.StartDate, c.Days, nil, challenge.Today(c.Timezone, b.now())).Ended {
		return challenge.ErrEnded
	}
	p, err := b.Posts.Get(ctx, postID, actor.ID)
	if err != nil {
		return err
	}
	if p.UserID != c.UserID {
		return challenge.ErrNotOwnPost
	}
	if err := b.Challenges.Link(ctx, id, postID); err != nil {
		return err
	}
	if p.IsPublic() {
		b.challengeDayRecorded(ctx, postID)
	}
	return nil
}

func (b *Blog) UnlinkChallengePost(ctx context.Context, id, postID int) error {
	ctx, span := tracer.Start(ctx, "Blog.UnlinkChallengePost")
	defer span.End()
	if _, err := b.ownChallenge(ctx, id); err != nil {
		return err
	}
	return b.Challenges.Unlink(ctx, id, postID)
}

// ActiveChallenges lists public running challenges.
func (b *Blog) ActiveChallenges(ctx context.Context, p domain.Paging) (challenge.Page, error) {
	ctx, span := tracer.Start(ctx, "Blog.ActiveChallenges")
	defer span.End()
	return b.Challenges.ListPublicActive(ctx, b.now(), p)
}

// MyChallenges lists all the actor's challenges.
func (b *Blog) MyChallenges(ctx context.Context, actor user.User, p domain.Paging) (challenge.Page, error) {
	ctx, span := tracer.Start(ctx, "Blog.MyChallenges")
	defer span.End()
	return b.Challenges.ListByUser(ctx, actor.ID, p)
}

// MyRunningChallenges lists the actor's running challenges with linked post ids.
func (b *Blog) MyRunningChallenges(ctx context.Context, actor user.User) ([]challenge.Challenge, error) {
	ctx, span := tracer.Start(ctx, "Blog.MyRunningChallenges")
	defer span.End()
	return b.Challenges.Running(ctx, actor.ID, b.now())
}

// PostChallenge is a readable post's day in its challenge; not found unless
// the post counts and the viewer may see the challenge.
func (b *Blog) PostChallenge(ctx context.Context, postID, viewerID int) (challenge.PostPlace, error) {
	ctx, span := tracer.Start(ctx, "Blog.PostChallenge")
	defer span.End()
	var none challenge.PostPlace
	if _, err := b.readable(ctx, postID, viewerID); err != nil {
		return none, err
	}
	c, err := b.Challenges.OfPost(ctx, postID)
	if err != nil {
		return none, err
	}
	if !b.canSee(ctx, c, viewerID) {
		return none, domain.ErrNotFound
	}
	days, err := b.Challenges.Days(ctx, c.ID)
	if err != nil {
		return none, err
	}
	i := slices.IndexFunc(days, func(d challenge.Day) bool { return d.PostID == postID })
	if i < 0 {
		return none, domain.ErrNotFound
	}
	return challenge.PostPlace{Challenge: c, Day: days[i].N, Of: c.Days}, nil
}

// Following and cheering

// othersChallenge loads a public challenge of someone else for the actor to
// follow or cheer.
func (b *Blog) othersChallenge(ctx context.Context, actor user.User, id int) (challenge.Challenge, error) {
	c, err := b.Challenges.Get(ctx, id)
	switch {
	case err != nil:
		return c, err
	case !b.canSee(ctx, c, actor.ID):
		return c, domain.ErrNotFound
	case c.UserID == actor.ID:
		return c, domain.Invalid("this is your own challenge")
	case c.Visibility != challenge.Public:
		return c, domain.Invalid("challenge is private")
	}
	return c, b.notBlocked(ctx, actor.ID, c.UserID)
}

func (b *Blog) FollowChallenge(ctx context.Context, actor user.User, id int) error {
	ctx, span := tracer.Start(ctx, "Blog.FollowChallenge")
	defer span.End()
	if err := b.Authz.Can(ctx, "follow", "create", 0, 0); err != nil {
		return err
	}
	if _, err := b.othersChallenge(ctx, actor, id); err != nil {
		return err
	}
	return b.Challenges.Follow(ctx, id, actor.ID)
}

func (b *Blog) UnfollowChallenge(ctx context.Context, actor user.User, id int) error {
	ctx, span := tracer.Start(ctx, "Blog.UnfollowChallenge")
	defer span.End()
	return b.Challenges.Unfollow(ctx, id, actor.ID)
}

// CheerChallenge cheers a running challenge, once per day of its zone.
func (b *Blog) CheerChallenge(ctx context.Context, actor user.User, id int) (challenge.Cheers, error) {
	ctx, span := tracer.Start(ctx, "Blog.CheerChallenge")
	defer span.End()
	var none challenge.Cheers
	if err := b.Authz.Can(ctx, "like", "create", id, 0); err != nil {
		return none, err
	}
	c, err := b.othersChallenge(ctx, actor, id)
	if err != nil {
		return none, err
	}
	today := challenge.Today(c.Timezone, b.now())
	if challenge.Compute(c.StartDate, c.Days, nil, today).Ended {
		return none, challenge.ErrEnded
	}
	if err := b.Challenges.Cheer(ctx, id, actor.ID, today); err != nil {
		return none, err
	}
	n, err := b.Challenges.CheersOn(ctx, id, today)
	if err != nil {
		return none, err
	}
	return challenge.Cheers{CheersToday: n, Cheered: true}, nil
}

// FollowedChallenges lists the public challenges the actor follows.
func (b *Blog) FollowedChallenges(ctx context.Context, actor user.User, p domain.Paging) (challenge.Page, error) {
	ctx, span := tracer.Start(ctx, "Blog.FollowedChallenges")
	defer span.End()
	return b.Challenges.ListFollowed(ctx, actor.ID, p)
}

// Notifications

// challengeDayRecorded announces a post that just became today's counted day
// of its challenge: to followers, to the owner and on milestones. Each message
// is sent once; errors are only logged.
func (b *Blog) challengeDayRecorded(ctx context.Context, postID int) {
	if b.Challenges == nil {
		return
	}
	if err := b.announceChallengeDay(ctx, postID); err != nil && !errors.Is(err, domain.ErrNotFound) {
		zap.L().Warn("challenge day", zap.Any("ctx", ctx), zap.Int("post", postID), zap.Error(err))
	}
}

func (b *Blog) announceChallengeDay(ctx context.Context, postID int) error {
	c, err := b.Challenges.OfPost(ctx, postID)
	if err != nil {
		return err
	}
	days, err := b.Challenges.Days(ctx, c.ID)
	if err != nil {
		return err
	}
	// An older post linked late is not news.
	today := challenge.Today(c.Timezone, b.now())
	i := slices.IndexFunc(days, func(d challenge.Day) bool { return d.PostID == postID && d.Date == today })
	if i < 0 {
		return nil
	}
	var followers []int
	if c.Visibility == challenge.Public {
		if followers, err = b.Challenges.FollowerIDs(ctx, c.ID); err != nil {
			return err
		}
	}
	note := func(t social.NotificationType, actorID int, text string) social.Notification {
		return social.Notification{Type: t, ActorID: actorID, ChallengeID: c.ID, Text: text}
	}
	var errs []error
	once := func(key string, send func()) {
		ok, err := b.Challenges.Claim(ctx, key)
		errs = append(errs, err)
		if ok {
			send()
		}
	}
	once("day:"+itoa(c.ID)+":"+today, func() {
		b.notifyLogged(ctx, note(social.NotifyChallengeDay, c.UserID, itoa(days[i].N)), followers...)
	})
	once("praise:"+itoa(c.UserID)+":"+today, func() {
		b.notifyLogged(ctx, note(social.NotifyChallengePraise, 0, variantText("praise", today)), c.UserID)
	})
	// Every reached milestone is claimed, so a skipped exact total is not
	// lost; only the highest newly reached one is announced.
	reached := 0
	for _, m := range challenge.Milestones {
		if len(days) >= m {
			once("ms:"+itoa(c.ID)+":"+itoa(m), func() { reached = max(reached, m) })
		}
	}
	if reached > 0 {
		b.notifyLogged(ctx, note(social.NotifyChallengeMilestone, c.UserID, itoa(reached)), followers...)
		b.notifyLogged(ctx, note(social.NotifyChallengeMilestone, 0, itoa(reached)), c.UserID)
	}
	return errors.Join(errs...)
}

func variantText(kind, date string) string {
	return kind + "." + strconv.Itoa(challenge.Variant(date))
}

// sentKeep is how long send-once keys are kept (milestone keys stay).
const sentKeep = 60 * 24 * time.Hour

// ChallengeReminders sends the due reminders, at most one per user per day
// (periodic job).
func (b *Blog) ChallengeReminders(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "Blog.ChallengeReminders")
	defer span.End()
	if b.Challenges == nil {
		return nil
	}
	now := b.now()
	list, err := b.Challenges.ReminderCandidates(ctx, now)
	if err != nil {
		return err
	}
	var errs []error
	for _, c := range list {
		if !c.Due(now) {
			continue
		}
		today := challenge.Today(c.Timezone, now)
		kind := challenge.NudgeKind(today, c.StartDate, c.Done)
		if kind == "" {
			continue
		}
		ok, err := b.Challenges.Claim(ctx, "remind:"+itoa(c.UserID)+":"+today)
		errs = append(errs, err)
		if ok {
			b.notifyLogged(ctx, social.Notification{Type: social.NotifyChallengeNudge, ChallengeID: c.ChallengeID,
				Text: variantText(kind, today)}, c.UserID)
		}
	}
	errs = append(errs, b.Challenges.PurgeSent(ctx, now.Add(-sentKeep)))
	return errors.Join(errs...)
}
