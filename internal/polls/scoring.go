package polls

// Ports src/lib/scoring.ts's scoreOptions/bestOptionId — the ranked-choice tally for a
// datetime/options poll (a signup poll never scores; see buildView in service.go).

var answerPoints = map[string]int{"yes": 2, "ifneedbe": 1, "no": 0}

// vote is the minimal shape scoreOptions needs; service.go builds these from queries.Vote rows.
type vote struct {
	optionID string
	answer   string
}

func scoreOptions(optionIDs []string, votes []vote) map[string]OptionScore {
	result := make(map[string]OptionScore, len(optionIDs))
	for _, id := range optionIDs {
		result[id] = OptionScore{}
	}
	for _, v := range votes {
		entry, ok := result[v.optionID]
		if !ok {
			continue
		}
		switch v.answer {
		case "yes":
			entry.Yes++
		case "ifneedbe":
			entry.IfNeedBe++
		case "no":
			entry.No++
		}
		entry.Score += answerPoints[v.answer]
		result[v.optionID] = entry
	}
	return result
}

func bestOptionID(orderedOptionIDs []string, scores map[string]OptionScore) *string {
	var bestID *string
	bestScore := 0
	for _, id := range orderedOptionIDs {
		score := scores[id].Score
		if score > bestScore {
			s := id
			bestScore = score
			bestID = &s
		}
	}
	return bestID
}
