package polls

import (
	"context"
	"strconv"

	"github.com/refsdal/whenweall/internal/polls/queries"
)

// buildView assembles a full PollView from a poll row plus its options/participants/votes/
// comments/organization name, computing scores/bestOptionId/claims the same way getPollView
// (service.ts) does. q may be tx-bound (mutating methods, mid-transaction) or the Service's own
// db-bound Queries (read-only methods).
//
// Notifications is always nil: it's the viewer's own per-poll notification override plus their
// account defaults, both of which live in notification_subscriptions/notification_prefs — tables
// Task 4 owns (UpdateNotificationPrefs/SetFollowing). IsOwner reflects only "viewerUserID created
// this poll" — the TS source's isOwner also lights up for any org admin/owner regardless of who
// created it (canManageContent), which needs the viewer's org role; Task 2's callers don't all
// carry that (see the doc comments on Update/SetStatus/Finalize/Delete), so this is a deliberate,
// narrower approximation. viewerUserID == "" (an anonymous/guest viewer, or a caller with no
// identity to offer) always yields IsOwner == false.
func (s *Service) buildView(ctx context.Context, q *queries.Queries, poll queries.Poll, viewerUserID string) (*PollView, error) {
	optionRows, err := q.ListOptionsByPoll(ctx, poll.ID)
	if err != nil {
		return nil, err
	}
	participantRows, err := q.ListParticipantsByPoll(ctx, poll.ID)
	if err != nil {
		return nil, err
	}
	voteRows, err := q.ListVotesByPoll(ctx, poll.ID)
	if err != nil {
		return nil, err
	}
	commentRows, err := q.ListCommentsByPoll(ctx, poll.ID)
	if err != nil {
		return nil, err
	}
	orgName, err := q.GetOrganizationName(ctx, poll.OrganizationID)
	if err != nil {
		return nil, err
	}

	optionIDs := make([]string, len(optionRows))
	options := make([]PollOptionView, len(optionRows))
	for i, o := range optionRows {
		optionIDs[i] = o.ID
		options[i] = optionToView(o)
	}

	votesByParticipant := make(map[string]map[string]string, len(participantRows))
	allVotes := make([]vote, 0, len(voteRows))
	for _, v := range voteRows {
		allVotes = append(allVotes, vote{optionID: v.OptionID, answer: v.Answer})
		m, ok := votesByParticipant[v.ParticipantID]
		if !ok {
			m = map[string]string{}
			votesByParticipant[v.ParticipantID] = m
		}
		m[v.OptionID] = v.Answer
	}

	participants := make([]ParticipantView, len(participantRows))
	for i, p := range participantRows {
		votes := votesByParticipant[p.ID]
		if votes == nil {
			votes = map[string]string{}
		}
		participants[i] = ParticipantView{
			ID:        p.ID,
			Name:      p.Name,
			UserID:    nullInt64ToStringPtr(p.UserID),
			HasEmail:  p.Email.Valid && p.Email.String != "",
			Votes:     votes,
			CreatedAt: formatISO(p.CreatedAt),
		}
	}

	comments := make([]CommentView, len(commentRows))
	for i, c := range commentRows {
		comments[i] = CommentView{
			ID:            c.ID,
			AuthorName:    c.AuthorName,
			Body:          c.Body,
			CreatedAt:     formatISO(c.CreatedAt),
			UserID:        nullInt64ToStringPtr(c.UserID),
			ParticipantID: nullStringPtr(c.ParticipantID),
		}
	}

	isSignup := poll.Type == string(PollTypeSignup)
	scores := map[string]OptionScore{}
	var best *string
	if !isSignup {
		scores = scoreOptions(optionIDs, allVotes)
		best = bestOptionID(optionIDs, scores)
	}

	claims := make(map[string]ClaimView, len(optionRows))
	for _, o := range optionRows {
		count := 0
		for _, v := range allVotes {
			if v.optionID == o.ID && v.answer == "yes" {
				count++
			}
		}
		capacity := nullInt32Ptr(o.Capacity)
		claims[o.ID] = ClaimView{
			Count:    count,
			Capacity: capacity,
			Full:     capacity != nil && count >= int(*capacity),
		}
	}

	isOwner := false
	if viewerUserID != "" && poll.CreatedBy.Valid {
		if strconv.FormatInt(poll.CreatedBy.Int64, 10) == viewerUserID {
			isOwner = true
		}
	}

	return &PollView{
		ID:                poll.ID,
		Type:              poll.Type,
		Title:             poll.Title,
		Description:       nullStringPtr(poll.Description),
		Location:          nullStringPtr(poll.Location),
		Timezone:          poll.Timezone,
		Status:            poll.Status,
		DeadlineAt:        nullTimeToISO(poll.DeadlineAt),
		FinalizedOptionID: nullStringPtr(poll.FinalizedOptionID),
		CreatedAt:         formatISO(poll.CreatedAt),
		Settings: PollSettingsView{
			RequireParticipantEmail: poll.RequireParticipantEmail,
			AllowComments:           poll.AllowComments,
			AllowIfNeedBe:           poll.AllowIfNeedBe,
			SignupMaxClaims:         poll.SignupMaxClaims,
		},
		Notifications: nil,
		Owner:         PollOwnerView{Name: orgName},
		IsOwner:       isOwner,
		Options:       options,
		Participants:  participants,
		Comments:      comments,
		Scores:        scores,
		BestOptionID:  best,
		Claims:        claims,
	}, nil
}

// optionToView converts one poll_options row to its view shape. A "date" kind option's startAt is
// rendered as a bare "YYYY-MM-DD" (the column is timestamptz, but the value stored is always
// midnight UTC for a date option — see pollOptionColumns); every other kind's startAt/endAt render
// as full ISO instants (nil when the column is NULL, which is always true for a "text" option).
func optionToView(o queries.PollOption) PollOptionView {
	view := PollOptionView{
		ID:       o.ID,
		Position: o.Position,
		Kind:     o.Kind,
		Label:    nullStringPtr(o.Label),
		Capacity: nullInt32Ptr(o.Capacity),
	}
	if o.Kind == string(OptionKindDate) && o.StartAt.Valid {
		s := formatDateOnly(o.StartAt.Time)
		view.StartAt = &s
		return view
	}
	view.StartAt = nullTimeToISO(o.StartAt)
	view.EndAt = nullTimeToISO(o.EndAt)
	return view
}
