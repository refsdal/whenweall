package polls

// Ports src/server/notifications/{recipients,subscriptions}.ts and the per-poll notification
// prefs/following halves of src/server/polls/service.ts (updateNotificationPrefs/
// setPollFollowing) — who gets mail for a poll event, and the two writes ("tune my grid for this
// poll", "follow/unfollow this poll") that shape it. The event catalog and per-event system
// defaults are ported from src/lib/notifications.ts.
//
// Push delivery is NOT ported: src/lib/notifications.ts's own recipients.ts resolves it gated by
// `entitlements.push` (a Premium-only org feature), and Go has no billing/entitlements service
// yet (emit.ts's own comment already calls push "Phase 2"). resolveRecipients here only ever
// returns email recipients; see the task report for the full rationale.
//
// User locale comes from the LocaleSource wired via SetLocaleSource (locale.go) — auth.Service's
// LocaleFor over user_preferences in production; participants keep their own locale column.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/refsdal/whenweall/internal/polls/queries"
)

// NotificationEvent mirrors the event union from src/lib/notifications.ts.
type NotificationEvent string

const (
	EventResponseCreated     NotificationEvent = "response.created"
	EventResponseUpdated     NotificationEvent = "response.updated"
	EventResponseWithdrawn   NotificationEvent = "response.withdrawn"
	EventCommentCreated      NotificationEvent = "comment.created"
	EventDeadlineApproaching NotificationEvent = "deadline.approaching"
	EventPollClosed          NotificationEvent = "poll.closed"
	EventPollFinalized       NotificationEvent = "poll.finalized"
	EventSignupFull          NotificationEvent = "signup.full"
)

// digestEvents mirrors DIGEST_EVENTS (src/lib/notifications.ts): events that batch through a
// debounced digest rather than sending immediately. Nothing in this task enqueues these from
// participant/comment activity yet (that wiring is out of this task's file list — see the task
// report) — EnqueueDigestItem (timers.go) exists as the produced capability for whichever task
// wires it up next.
var digestEvents = map[NotificationEvent]bool{
	EventResponseCreated:   true,
	EventResponseUpdated:   true,
	EventResponseWithdrawn: true,
	EventCommentCreated:    true,
	EventSignupFull:        true,
}

func isDigestEvent(event NotificationEvent) bool { return digestEvents[event] }

// ChannelPrefs mirrors ChannelPrefs (src/lib/notifications.ts): whether an event reaches a given
// channel at all.
type ChannelPrefs struct {
	Email bool `json:"email"`
	Push  bool `json:"push"`
}

// NotificationGrid mirrors NotificationGrid: a per-event override, stored as the `channels` jsonb
// column on both notification_subscriptions (per-poll override) and notification_prefs (per-user
// default) — nil means "no override at this level, fall through".
type NotificationGrid map[NotificationEvent]ChannelPrefs

// systemDefaults mirrors SYSTEM_DEFAULTS (src/lib/notifications.ts) — the grid a user has before
// ever opening settings.
var systemDefaults = map[NotificationEvent]ChannelPrefs{
	EventResponseCreated:     {Email: true, Push: true},
	EventResponseUpdated:     {Email: true, Push: false},
	EventResponseWithdrawn:   {Email: false, Push: false},
	EventCommentCreated:      {Email: true, Push: true},
	EventDeadlineApproaching: {Email: true, Push: true},
	EventPollClosed:          {Email: true, Push: false},
	EventPollFinalized:       {Email: true, Push: false},
	EventSignupFull:          {Email: true, Push: false},
}

// resolveChannels mirrors resolveChannels (src/lib/notifications.ts): scope override -> user
// default -> system default, resolved per event key so overriding one event never resets the
// others.
func resolveChannels(event NotificationEvent, override, defaults NotificationGrid) ChannelPrefs {
	if override != nil {
		if v, ok := override[event]; ok {
			return v
		}
	}
	if defaults != nil {
		if v, ok := defaults[event]; ok {
			return v
		}
	}
	return systemDefaults[event]
}

func decodeGrid(raw json.RawMessage) (NotificationGrid, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var g NotificationGrid
	if err := json.Unmarshal(raw, &g); err != nil {
		return nil, err
	}
	return g, nil
}

func encodeGrid(g NotificationGrid) (json.RawMessage, error) {
	if g == nil {
		return nil, nil
	}
	return json.Marshal(g)
}

// Recipient is one resolved mail recipient — ported from Recipient (recipients.ts), email-only
// (see this file's package doc comment for why push isn't here).
type Recipient struct {
	UserID string
	Email  string
	Name   string
	Locale string
}

// resolveRecipients ports resolveRecipients (recipients.ts): every subscriber to (scopeType,
// pollID) who is still an org member (membership is the authority, not the subscription row —
// someone who left the org can no longer open the poll and must not keep hearing about it),
// minus actorUserID, with the event's channel resolved per resolveChannels, filtered to those
// with the email channel on.
//
// localeMemo is passed straight to userLocaleMemo for each resolved recipient's Locale field: nil
// for every call site that only ever calls this once per job (handleDeadlineJob,
// handleReminderJob, Finalize's own notification), a shared map for fanOutDigestItems's per-event
// loop, so a recipient subscribed to more than one of a digest's distinct events pays for one
// LocaleSource lookup for the whole job, not one per event — see fanOutDigestItems's own doc
// comment for why that round trip specifically matters here (it runs inside the advisory-locked
// transaction processDigestJob holds).
func (s *Service) resolveRecipients(
	ctx context.Context, q *queries.Queries, orgID int64, pollID string, event NotificationEvent, actorUserID string,
	localeMemo map[string]string,
) ([]Recipient, error) {
	subs, err := q.ListSubscriptionsByScope(ctx, queries.ListSubscriptionsByScopeParams{
		ScopeType: "poll", ScopeID: pollID,
	})
	if err != nil {
		return nil, err
	}

	out := make([]Recipient, 0, len(subs))
	for _, sub := range subs {
		uid := strconv.FormatInt(sub.UserID, 10)
		if uid == actorUserID {
			continue
		}

		isMember, err := q.IsOrgMember(ctx, queries.IsOrgMemberParams{OrganizationID: orgID, UserID: sub.UserID})
		if err != nil {
			return nil, err
		}
		if !isMember {
			continue
		}

		u, err := q.GetUser(ctx, sub.UserID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}

		var subGrid, prefGrid NotificationGrid
		if sub.Channels.Valid {
			if subGrid, err = decodeGrid(sub.Channels.RawMessage); err != nil {
				return nil, err
			}
		}
		pref, perr := q.GetNotificationPref(ctx, sub.UserID)
		switch {
		case perr == nil && pref.Channels.Valid:
			if prefGrid, err = decodeGrid(pref.Channels.RawMessage); err != nil {
				return nil, err
			}
		case perr != nil && !errors.Is(perr, sql.ErrNoRows):
			return nil, perr
		}

		ch := resolveChannels(event, subGrid, prefGrid)
		if !ch.Email {
			continue
		}

		out = append(out, Recipient{UserID: uid, Email: u.Email, Name: displayName(u), Locale: s.userLocaleMemo(ctx, uid, localeMemo)})
	}
	return out, nil
}

// UpdateNotificationPrefs ports updateNotificationPrefs (service.ts): writes the caller's own
// per-poll channel override, implicitly following the poll first (tuning a poll you haven't
// followed yet must not silently land on no row).
//
// Deviation from the brief's literal signature (`UpdateNotificationPrefs(ctx, userID string,
// channels map[string]bool) error`): the brief omitted pollID and orgID, but TS's own
// updateNotificationPrefs requires both (NOT_FOUND for a missing/deleted poll, FORBIDDEN for a
// wrong-org one) — dropping them would make every managing call in this package inconsistent
// with itself, so both are added, following requireOrgPoll's own established pattern (Finalize,
// SetStatus, ...). channels is NotificationGrid (map[event]{email,push}), not map[string]bool: a
// single bool can't express what's actually stored in the jsonb column and read back by
// resolveRecipients.
func (s *Service) UpdateNotificationPrefs(ctx context.Context, pollID, orgID, userID string, channels NotificationGrid) error {
	userIDInt, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		return ErrForbidden
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := queries.New(tx)

	if _, err := requireOrgPoll(ctx, q, pollID, orgID); err != nil {
		return err
	}

	now := time.Now().UTC()
	if err := q.UpsertNotificationSubscription(ctx, queries.UpsertNotificationSubscriptionParams{
		ScopeType: "poll", ScopeID: pollID, UserID: userIDInt, Source: "follow", CreatedAt: now,
	}); err != nil {
		return err
	}

	raw, err := encodeGrid(channels)
	if err != nil {
		return err
	}
	if err := q.SetNotificationSubscriptionChannels(ctx, queries.SetNotificationSubscriptionChannelsParams{
		ScopeType: "poll", ScopeID: pollID, UserID: userIDInt, Column4: raw, UpdatedAt: now,
	}); err != nil {
		return err
	}

	return tx.Commit()
}

// SetFollowing ports setPollFollowing (service.ts): follow/unfollow a poll. Any member of the
// poll's own org may call this (matching the TS doc comment — no extra managing check).
func (s *Service) SetFollowing(ctx context.Context, pollID, orgID, userID string, following bool) error {
	userIDInt, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		return ErrForbidden
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := queries.New(tx)

	if _, err := requireOrgPoll(ctx, q, pollID, orgID); err != nil {
		return err
	}

	if following {
		if err := q.UpsertNotificationSubscription(ctx, queries.UpsertNotificationSubscriptionParams{
			ScopeType: "poll", ScopeID: pollID, UserID: userIDInt, Source: "follow", CreatedAt: time.Now().UTC(),
		}); err != nil {
			return err
		}
	} else {
		if err := q.DeleteNotificationSubscription(ctx, queries.DeleteNotificationSubscriptionParams{
			ScopeType: "poll", ScopeID: pollID, UserID: userIDInt,
		}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ensureCreatorSubscriptionBestEffort ports ensureCreatorSubscription (subscriptions.ts), called
// by Create/Duplicate AFTER their own transaction has already committed — "outside the batch on
// purpose" per the TS source: the creator's subscription is a notification convenience, not part
// of the poll's integrity, so a failure here must never roll back (or fail) poll creation itself.
// userID is nullable because CreatedBy is (a poll whose creator later deletes their account has
// nobody to subscribe — not an error).
func (s *Service) ensureCreatorSubscriptionBestEffort(ctx context.Context, pollID string, userID sql.NullInt64) {
	if !userID.Valid {
		return
	}
	if err := s.q.UpsertNotificationSubscription(ctx, queries.UpsertNotificationSubscriptionParams{
		ScopeType: "poll", ScopeID: pollID, UserID: userID.Int64, Source: "creator", CreatedAt: time.Now().UTC(),
	}); err != nil {
		slog.Default().Error("polls: ensureCreatorSubscription failed", "pollId", pollID, "error", err)
	}
}

// copyNotificationOverrideBestEffort ports duplicatePoll's originalOverride carry-over
// (service.ts): if the duplicator had tuned their own channel override on the original poll,
// carry it to the copy — without this, duplicating a poll silently resets it to defaults. Also
// best-effort, also called post-commit; requires ensureCreatorSubscriptionBestEffort to have run
// first so the copy already has a subscription row to set channels on.
func (s *Service) copyNotificationOverrideBestEffort(ctx context.Context, originalPollID, newPollID string, userID int64) {
	subs, err := s.q.ListSubscriptionsByScope(ctx, queries.ListSubscriptionsByScopeParams{
		ScopeType: "poll", ScopeID: originalPollID,
	})
	if err != nil {
		slog.Default().Error("polls: copyNotificationOverride: list original subscriptions failed", "pollId", originalPollID, "error", err)
		return
	}
	for _, sub := range subs {
		if sub.UserID != userID || !sub.Channels.Valid {
			continue
		}
		if err := s.q.SetNotificationSubscriptionChannels(ctx, queries.SetNotificationSubscriptionChannelsParams{
			ScopeType: "poll", ScopeID: newPollID, UserID: userID,
			Column4: sub.Channels.RawMessage, UpdatedAt: time.Now().UTC(),
		}); err != nil {
			slog.Default().Error("polls: copyNotificationOverride: set channels failed", "pollId", newPollID, "error", err)
		}
		return
	}
}

// DigestItem is one queued activity item for a poll's debounced digest — ports DigestItem
// (src/do/protocol.ts) / the payload shape queueDigest (do-client.ts) hands PollRoom. No address:
// only the event, the actor's display name (for "3 new responses — Ada, Bob"), and the actor's
// user id (so the digest can exclude items the recipient caused themselves) — matching the
// mailer privacy rule (internal/mailer's Enqueue doc comment): entity-mail payloads never carry
// an address.
type DigestItem struct {
	Event       NotificationEvent `json:"event"`
	Name        string            `json:"name,omitempty"`
	ActorUserID string            `json:"actorUserId,omitempty"`
}

// digestPayload is the "poll.digest" job's payload shape: the accumulating item list for one
// poll's debounce window (timers.go's EnqueueDigestItem/processDigestJob).
type digestPayload struct {
	PollID string       `json:"pollId"`
	Items  []DigestItem `json:"items"`
}

// displayName builds a recipient's display name from a Go `users` row, which (unlike Drizzle's
// `user.name`) has no single name column — only nullable FirstName/LastName. Falls back to the
// email's local part, then the raw email, if both are blank.
func displayName(u queries.User) string {
	first := ""
	if u.FirstName.Valid {
		first = strings.TrimSpace(u.FirstName.String)
	}
	last := ""
	if u.LastName.Valid {
		last = strings.TrimSpace(u.LastName.String)
	}
	name := strings.TrimSpace(strings.TrimSpace(first + " " + last))
	if name != "" {
		return name
	}
	if i := strings.IndexByte(u.Email, '@'); i > 0 {
		return u.Email[:i]
	}
	return u.Email
}

// orDefaultLocale returns l if set, else "en" — participants (unlike users) do carry their own
// locale column.
func orDefaultLocale(l sql.NullString) string {
	if l.Valid && l.String != "" {
		return l.String
	}
	return "en"
}
