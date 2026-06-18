package quiz

// RoundProgress describes where a question sits among a quiz's rounds: which
// round it belongs to (1-indexed, out of the quiz's round count) and where it
// falls within that round (1-indexed, out of the round's question count). It
// lets a gameplay surface show a "Round N of M" heading and a per-round
// "Question n of m" chip without the surface re-deriving the round grouping.
type RoundProgress struct {
	RoundNumber    int
	RoundTotal     int
	RoundPosition  int
	RoundQuestions int
}

// QuestionRoundProgress derives the [RoundProgress] for questionID from the
// quiz's questions, which carry their round_id and are taken in quiz-wide
// position order. Rounds are numbered by the order their first question appears
// in that sequence, so the numbering matches the order the questions are played
// without needing the separate rounds list loaded. Returns the zero value when
// questionID is not among questions (a deleted question mid-game), so a surface
// falls back to its generic copy rather than naming a stale round.
func QuestionRoundProgress(questions []*Question, questionID int64) RoundProgress {
	roundOrder := make([]int64, 0)
	seen := make(map[int64]bool)
	counts := make(map[int64]int)
	for _, q := range questions {
		if !seen[q.RoundID] {
			seen[q.RoundID] = true
			roundOrder = append(roundOrder, q.RoundID)
		}
		counts[q.RoundID]++
	}

	for _, q := range questions {
		if q.ID != questionID {
			continue
		}
		roundNumber := 0
		for i, id := range roundOrder {
			if id == q.RoundID {
				roundNumber = i + 1

				break
			}
		}
		roundPosition := 0
		for _, sib := range questions {
			if sib.RoundID == q.RoundID {
				roundPosition++
				if sib.ID == q.ID {
					break
				}
			}
		}

		return RoundProgress{
			RoundNumber:    roundNumber,
			RoundTotal:     len(roundOrder),
			RoundPosition:  roundPosition,
			RoundQuestions: counts[q.RoundID],
		}
	}

	return RoundProgress{}
}
