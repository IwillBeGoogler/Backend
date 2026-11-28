package http

import (
	"net/http"
	"strconv"

	"github.com/IWillBeGoogler/iwillbegoogler-monolith-go/internal/domain/challenge"
)

const challengeNotFound = "challenge not found"

// listChallenges lists challenges, newest start first.
//
// ?tab= "active" (default: public running ones), "mine" (all of the
// caller's) or "following" (public ones the caller follows). ?cursor= and ?limit= page the list.
// Auth: none for active, signed in for mine and following.
//
// spector:tags challenges
func (h *Handler) listChallenges(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Query().Get("tab") {
	case "", "active":
		page, err := h.Blog.ActiveChallenges(r.Context(), paging(r))
		respond(w, r, http.StatusOK, page, err, "")
	case "mine":
		if actor, ok := h.requireUser(w, r); ok {
			page, err := h.Blog.MyChallenges(r.Context(), actor, paging(r))
			respond(w, r, http.StatusOK, page, err, "")
		}
	case "following":
		if actor, ok := h.requireUser(w, r); ok {
			page, err := h.Blog.FollowedChallenges(r.Context(), actor, paging(r))
			respond(w, r, http.StatusOK, page, err, "")
		}
	default:
		writeError(w, r, http.StatusBadRequest, "tab must be active, mine or following")
	}
}

// myRunningChallenges lists the caller's running challenges with the ids of
// their linked posts.
//
// Auth: signed in.
//
// spector:tags challenges
func (h *Handler) myRunningChallenges(w http.ResponseWriter, r *http.Request) {
	if actor, ok := h.requireUser(w, r); ok {
		list, err := h.Blog.MyRunningChallenges(r.Context(), actor)
		respond(w, r, http.StatusOK, list, err, "")
	}
}

// createChallenge starts a challenge today (in the given time zone).
//
// days 7-365, at most 3 running per user. days, timezone and the start date
// cannot be changed later.
// Auth: post.create.
//
// spector:tags challenges
func (h *Handler) createChallenge(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	var in challenge.Input
	if ok && decode(w, r, &in) {
		c, err := h.Blog.CreateChallenge(r.Context(), actor, in)
		respond(w, r, http.StatusCreated, c, err, "")
	}
}

// getChallenge returns a challenge with its stats and counted days.
//
// A day counts for the first published post of at least 300 characters on
// that date. Private challenges are 404 to others.
// Auth: optional.
//
// spector:tags challenges
func (h *Handler) getChallenge(w http.ResponseWriter, r *http.Request) {
	if id, ok := pathID(w, r); ok {
		d, err := h.Blog.GetChallenge(r.Context(), id, h.viewerID(r))
		respond(w, r, http.StatusOK, d, err, challengeNotFound)
	}
}

// updateChallenge changes title, description, visibility, reminders (bool)
// and remind_at ("HH:MM").
//
// Fields left out keep their values; days and timezone are ignored.
// Auth: challenge owner or post.update.
//
// spector:tags challenges
func (h *Handler) updateChallenge(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	cur, err := h.Blog.GetChallenge(r.Context(), id, h.viewerID(r))
	if err != nil {
		respond(w, r, 0, nil, err, challengeNotFound)
		return
	}
	in := challenge.Input{Title: cur.Title, Description: cur.Description, Visibility: cur.Visibility}
	if decode(w, r, &in) {
		c, err := h.Blog.UpdateChallenge(r.Context(), id, in)
		respond(w, r, http.StatusOK, c, err, challengeNotFound)
	}
}

// deleteChallenge deletes a challenge; its posts stay.
//
// Auth: challenge owner or post.update.
//
// spector:tags challenges
func (h *Handler) deleteChallenge(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); !ok {
		return
	}
	if id, ok := pathID(w, r); ok {
		noContent(w, r, h.Blog.DeleteChallenge(r.Context(), id), challengeNotFound)
	}
}

func pathPostID(w http.ResponseWriter, r *http.Request) (int, bool) {
	id, err := strconv.Atoi(r.PathValue("postId"))
	if err != nil || id <= 0 {
		writeError(w, r, http.StatusBadRequest, "bad post id")
		return 0, false
	}
	return id, true
}

// linkChallengePost links a post (drafts allowed) to a running challenge.
//
// The post must be the challenge owner's (400) and in no other challenge
// (409). Repeating the call is a no-op.
// Auth: challenge owner or post.update.
//
// spector:tags challenges
func (h *Handler) linkChallengePost(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if postID, ok := pathPostID(w, r); ok {
		noContent(w, r, h.Blog.LinkChallengePost(r.Context(), actor, id, postID), "challenge or post not found")
	}
}

// unlinkChallengePost removes a post from a challenge.
//
// Auth: challenge owner or post.update.
//
// spector:tags challenges
func (h *Handler) unlinkChallengePost(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireUser(w, r); !ok {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if postID, ok := pathPostID(w, r); ok {
		noContent(w, r, h.Blog.UnlinkChallengePost(r.Context(), id, postID), "post not in this challenge")
	}
}

// postChallenge returns the day a post counts for in its challenge.
//
// 404 when the post counts in no challenge the caller may see.
// Auth: optional.
//
// spector:tags challenges
func (h *Handler) postChallenge(w http.ResponseWriter, r *http.Request) {
	if id, ok := pathID(w, r); ok {
		p, err := h.Blog.PostChallenge(r.Context(), id, h.viewerID(r))
		respond(w, r, http.StatusOK, p, err, "not in a challenge")
	}
}

// followChallenge follows someone's public challenge; repeating is a no-op.
//
// 400 for your own or a private one.
// Auth: follow.create.
//
// spector:tags challenges
func (h *Handler) followChallenge(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	if id, ok := pathID(w, r); ok {
		noContent(w, r, h.Blog.FollowChallenge(r.Context(), actor, id), challengeNotFound)
	}
}

// unfollowChallenge stops following; a no-op when not following.
//
// Auth: signed in.
//
// spector:tags challenges
func (h *Handler) unfollowChallenge(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	if id, ok := pathID(w, r); ok {
		noContent(w, r, h.Blog.UnfollowChallenge(r.Context(), actor, id), challengeNotFound)
	}
}

// cheerChallenge cheers a running public challenge, once per day.
//
// Returns today's count; repeating is a no-op. 400 for your own, a private or
// an ended one.
// Auth: like.create.
//
// spector:tags challenges
func (h *Handler) cheerChallenge(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.requireUser(w, r)
	if !ok {
		return
	}
	if id, ok := pathID(w, r); ok {
		c, err := h.Blog.CheerChallenge(r.Context(), actor, id)
		respond(w, r, http.StatusOK, c, err, challengeNotFound)
	}
}
