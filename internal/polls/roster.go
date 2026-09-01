package polls

// Ports src/server/polls/roster.ts's buildRosterCsv: the owner-only roster export for a sign-up
// sheet (one row per claim, plus a single zero-claim row for a still-open slot).
//
// Deviation: the TS source's buildRosterCsv takes a locale and renders each slot's label via
// formatOptionLabel (Intl.DateTimeFormat-aware). This port reuses optionLabelText (timers.go) —
// the same plain-English, non-locale-aware renderer already used for transactional mail bodies —
// rather than a locale-aware Go port of formatOptionLabel, which doesn't exist yet. See
// timers.go's optionLabelText doc comment for the same flagged follow-up.
import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/refsdal/whenweall/internal/polls/queries"
)

// rosterBOM is the UTF-8 byte-order mark buildRosterCsv (TS) prefixes the file with, so
// spreadsheet apps that sniff encoding (notably Excel) render non-ASCII names correctly.
const rosterBOM = "\uFEFF"

// rosterFormulaPrefix ports escapeFormula's detector (roster.ts): a field starting with
// = + - @ (or a leading tab/CR) is read as a formula by Excel/Sheets/LibreOffice once the CSV is
// opened — prefixing it with a single quote defuses that without changing how it displays.
var rosterFormulaPrefix = regexp.MustCompile(`^[=+\-@\t\r]`)

// rosterQuoteNeeded ports csvField's quoting trigger (roster.ts): quote whenever the field
// contains a comma, a double quote, or a line break (RFC 4180).
var rosterQuoteNeeded = regexp.MustCompile(`[",\r\n]`)

// rosterCSVField ports escapeFormula + csvField (roster.ts).
func rosterCSVField(raw string) string {
	value := raw
	if rosterFormulaPrefix.MatchString(value) {
		value = "'" + value
	}
	if rosterQuoteNeeded.MatchString(value) {
		return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
	}
	return value
}

func rosterCSVRow(fields []string) string {
	out := make([]string, len(fields))
	for i, f := range fields {
		out[i] = rosterCSVField(f)
	}
	return strings.Join(out, ",")
}

// BuildRosterCSV ports buildRosterCsv (roster.ts): one row per claim (slot, capacity, total
// claimed, participant name, email), plus a single zero-claim row for a slot nobody has taken yet
// (empty participant/email). Returns ErrNotFound for a missing/soft-deleted poll — the caller
// (Task 7's HTTP handler) is expected to have already checked the caller may manage this poll
// (roster export is owner-only, per the brief's "auth+org" row).
func (s *Service) BuildRosterCSV(ctx context.Context, pollID string) (string, error) {
	poll, err := s.q.GetPoll(ctx, pollID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}

	options, err := s.q.ListOptionsByPoll(ctx, pollID)
	if err != nil {
		return "", err
	}
	participants, err := s.q.ListParticipantsByPoll(ctx, pollID)
	if err != nil {
		return "", err
	}
	votes, err := s.q.ListVotesByPoll(ctx, pollID)
	if err != nil {
		return "", err
	}

	yesByOption := make(map[string][]string) // optionID -> participantIDs (yes votes only)
	for _, v := range votes {
		if v.Answer == "yes" {
			yesByOption[v.OptionID] = append(yesByOption[v.OptionID], v.ParticipantID)
		}
	}
	participantByID := make(map[string]queries.Participant, len(participants))
	for _, p := range participants {
		participantByID[p.ID] = p
	}

	rows := []string{rosterCSVRow([]string{"slot", "capacity", "claimed", "participant", "email"})}

	for _, option := range options {
		slotLabel := optionLabelText(option, poll.Timezone)
		capacity := ""
		if option.Capacity.Valid {
			capacity = strconv.Itoa(int(option.Capacity.Int32))
		}

		claimantIDs := yesByOption[option.ID]
		claimed := strconv.Itoa(len(claimantIDs))

		if len(claimantIDs) == 0 {
			rows = append(rows, rosterCSVRow([]string{slotLabel, capacity, claimed, "", ""}))
			continue
		}
		for _, pid := range claimantIDs {
			p := participantByID[pid]
			email := ""
			if p.Email.Valid {
				email = p.Email.String
			}
			rows = append(rows, rosterCSVRow([]string{slotLabel, capacity, claimed, p.Name, email}))
		}
	}

	return rosterBOM + strings.Join(rows, "\r\n") + "\r\n", nil
}
