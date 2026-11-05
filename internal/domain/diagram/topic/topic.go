// Package topic is the MQTT topic space of the diagram workspace's realtime
// channels: building topics, parsing the ones clients publish or subscribe
// to, and the shape of client ids. docs/MQTT.md is the protocol reference.
//
//	dg/p/{project}/events                  server -> clients   tree.updated, file.updated, library.updated, ctl
//	dg/p/{project}/roster/{file}/{client}  server -> clients   retained: who has which file open
//	dg/f/{file}/events                     server -> clients   file.updated, comment.*, rtc.*, ctl
//	dg/f/{file}/roster/{client}            server -> clients   retained: who has this file open
//	dg/f/{file}/yjs/up/{client}            client -> server    Yjs updates and compacted snapshots (editors)
//	dg/f/{file}/yjs/req/{client}           client -> server    sync step 1 (any role)
//	dg/f/{file}/yjs/down                   server -> clients   Yjs updates other editors made
//	dg/f/{file}/yjs/sync/{client}          server -> client    join answer, compaction hints
//	dg/f/{file}/awareness/up/{client}      client -> server    cursors
//	dg/f/{file}/awareness/out              server -> clients   cursors with the identity the server vouches for
//	dg/f/{file}/presence/{client}          client -> server    retained, with a Last Will that clears it
//	dg/f/{file}/rtc/up/{client}            client -> server    audio room commands
//	dg/f/{file}/rtc/{client}               server -> client    audio room signaling
//	dg/c/{client}/ctl                      server -> client    denied, unavailable, rtc.full
//	dg/s/acl                               server -> servers   clients whose cached access is stale (never parsed: no client may use it)
package topic

import (
	"regexp"
	"strconv"
	"strings"
)

// Kind says which channel a topic is.
type Kind int

const (
	Unknown Kind = iota
	ProjectEvents
	ProjectRoster
	FileEvents
	FileRoster
	YjsUp
	YjsReq
	YjsDown
	YjsSync
	AwarenessUp
	AwarenessOut
	Presence
	RTCUp
	RTCInbox
	Ctl
)

// Topic is a parsed topic. Wildcard parts of a filter are "+".
type Topic struct {
	Kind      Kind
	ProjectID string
	FileID    string
	ClientID  string
}

// Wildcard is the single-level wildcard.
const Wildcard = "+"

var idRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,80}$`)

func id(s string, wild bool) bool { return idRe.MatchString(s) || wild && s == Wildcard }

// Parse reads a concrete topic (what a client publishes to).
func Parse(topic string) (Topic, bool) { return parse(topic, false) }

// ParseFilter reads a topic filter (what a client subscribes to). Only the
// roster topics take wildcards, in their id positions.
func ParseFilter(filter string) (Topic, bool) { return parse(filter, true) }

func parse(s string, filter bool) (Topic, bool) {
	p := strings.Split(s, "/")
	if len(p) < 3 || p[0] != "dg" {
		return Topic{}, false
	}
	t, ok := Topic{}, false
	switch p[1] {
	case "p":
		t.ProjectID = p[2]
		switch {
		case len(p) == 4 && p[3] == "events":
			t.Kind, ok = ProjectEvents, true
		case len(p) == 6 && p[3] == "roster":
			t.Kind, t.FileID, t.ClientID, ok = ProjectRoster, p[4], p[5], true
		}
		if ok && !id(t.ProjectID, false) {
			ok = false
		}
		if ok && t.Kind == ProjectRoster && !(id(t.FileID, filter) && id(t.ClientID, filter)) {
			ok = false
		}
	case "f":
		t.FileID = p[2]
		switch {
		case len(p) == 4 && p[3] == "events":
			t.Kind, ok = FileEvents, true
		case len(p) == 5 && p[3] == "roster":
			t.Kind, t.ClientID, ok = FileRoster, p[4], id(p[4], filter)
		case len(p) == 5 && p[3] == "yjs" && p[4] == "down":
			t.Kind, ok = YjsDown, true
		case len(p) == 6 && p[3] == "yjs" && p[4] == "up":
			t.Kind, t.ClientID, ok = YjsUp, p[5], id(p[5], false)
		case len(p) == 6 && p[3] == "yjs" && p[4] == "req":
			t.Kind, t.ClientID, ok = YjsReq, p[5], id(p[5], false)
		case len(p) == 6 && p[3] == "yjs" && p[4] == "sync":
			t.Kind, t.ClientID, ok = YjsSync, p[5], id(p[5], false)
		case len(p) == 6 && p[3] == "awareness" && p[4] == "up":
			t.Kind, t.ClientID, ok = AwarenessUp, p[5], id(p[5], false)
		case len(p) == 5 && p[3] == "awareness" && p[4] == "out":
			t.Kind, ok = AwarenessOut, true
		case len(p) == 5 && p[3] == "presence":
			t.Kind, t.ClientID, ok = Presence, p[4], id(p[4], false)
		case len(p) == 6 && p[3] == "rtc" && p[4] == "up":
			t.Kind, t.ClientID, ok = RTCUp, p[5], id(p[5], false)
		case len(p) == 5 && p[3] == "rtc":
			t.Kind, t.ClientID, ok = RTCInbox, p[4], id(p[4], false)
		}
		if ok && !id(t.FileID, false) {
			ok = false
		}
	case "c":
		if len(p) == 4 && p[3] == "ctl" {
			t.Kind, t.ClientID, ok = Ctl, p[2], id(p[2], false)
		}
	}
	if !ok {
		return Topic{}, false
	}
	return t, true
}

// Builders

func ProjectEventsTopic(project string) string { return "dg/p/" + project + "/events" }
func ProjectRosterTopic(project, file, client string) string {
	return "dg/p/" + project + "/roster/" + file + "/" + client
}
func FileEventsTopic(file string) string          { return "dg/f/" + file + "/events" }
func FileRosterTopic(file, client string) string  { return "dg/f/" + file + "/roster/" + client }
func YjsDownTopic(file string) string             { return "dg/f/" + file + "/yjs/down" }
func YjsSyncTopic(file, client string) string     { return "dg/f/" + file + "/yjs/sync/" + client }
func AwarenessOutTopic(file string) string        { return "dg/f/" + file + "/awareness/out" }
func RTCInboxTopic(file, client string) string    { return "dg/f/" + file + "/rtc/" + client }
func YjsUpTopic(file, client string) string       { return "dg/f/" + file + "/yjs/up/" + client }
func YjsReqTopic(file, client string) string      { return "dg/f/" + file + "/yjs/req/" + client }
func AwarenessUpTopic(file, client string) string { return "dg/f/" + file + "/awareness/up/" + client }
func PresenceTopic(file, client string) string    { return "dg/f/" + file + "/presence/" + client }
func RTCUpTopic(file, client string) string       { return "dg/f/" + file + "/rtc/up/" + client }
func CtlTopic(client string) string               { return "dg/c/" + client + "/ctl" }

// Server subscriptions: the filters the API instances share work on. All
// instances join the shared group Group, so each message reaches one.
const Group = "dg"

var ServerFilters = []string{
	"dg/f/+/yjs/up/+",
	"dg/f/+/yjs/req/+",
	"dg/f/+/awareness/up/+",
	"dg/f/+/presence/+",
	"dg/f/+/rtc/up/+",
}

// ACLDrop is the topic API instances tell each other which clients' cached
// access to drop. Parse does not know it, so the access rules refuse it to
// every browser client.
const ACLDrop = "dg/s/acl"

// Shared is the shared-subscription form of a filter.
func Shared(filter string) string { return "$share/" + Group + "/" + filter }

// Match reports whether topic matches filter (MQTT wildcards; a $share/group/
// prefix on the filter is ignored).
func Match(filter, topic string) bool {
	if rest, ok := strings.CutPrefix(filter, "$share/"); ok {
		if _, f, ok := strings.Cut(rest, "/"); ok {
			filter = f
		}
	}
	fs, ts := strings.Split(filter, "/"), strings.Split(topic, "/")
	for i, f := range fs {
		if f == "#" {
			return true
		}
		if i >= len(ts) || f != "+" && f != ts[i] {
			return false
		}
	}
	return len(fs) == len(ts)
}

// Client ids

// A client id says who a connection is, so it cannot be borrowed: the broker
// authentication only lets a ticket for user N connect as "u<N>.<nonce>" and a
// share link holder as "g.<nonce>"; the access rules only let a client use
// topics that carry its own id. The nonce makes it unique per browser tab.
var nonceRe = regexp.MustCompile(`^[A-Za-z0-9_-]{8,48}$`)

// ClientID builds the client id of a signed-in user (user 0: a share-link guest).
func ClientID(user int, nonce string) string {
	if user == 0 {
		return "g." + nonce
	}
	return "u" + strconv.Itoa(user) + "." + nonce
}

// ParseClientID reads a browser client id: user is 0 for a guest.
func ParseClientID(clientID string) (user int, ok bool) {
	head, nonce, found := strings.Cut(clientID, ".")
	if !found || !nonceRe.MatchString(nonce) {
		return 0, false
	}
	if head == "g" {
		return 0, true
	}
	n, err := strconv.Atoi(strings.TrimPrefix(head, "u"))
	if !strings.HasPrefix(head, "u") || err != nil || n <= 0 || ClientID(n, nonce) != clientID {
		return 0, false
	}
	return n, true
}
