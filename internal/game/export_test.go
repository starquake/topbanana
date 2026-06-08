package game

import (
	"context"
	"time"

	"github.com/starquake/topbanana/internal/quiz"
)

// Test-only re-exports of internal helpers (#: blackbox-test sweep
// against the "prefer dot-import blackbox tests" rule in the
// backend-dev agent). The wrapped identifiers stay unexported so the
// production surface is unchanged; only the external game_test
// package sees the Export* names.
var (
	ExportClampTappedAt       = clampTappedAt
	ExportResolveAnswerWindow = resolveAnswerWindow
	ExportDefaultExpiration   = defaultExpiration
)

// ExportRoundSlot is the test-visible projection of the unexported
// roundSlot returned by nextRoundSlot. Kind is empty when the walk
// reached the end with nothing left to emit.
type ExportRoundSlot struct {
	Kind     string
	Question *quiz.Question
	Round    *quiz.Round
	Phase    RoundPhase
}

// ExportNextRoundSlot drives the unexported round-walking iterator from
// the external test package. seen lists the (round, phase) pairs the
// player has acknowledged.
func ExportNextRoundSlot(
	rounds []*quiz.Round,
	questions []*quiz.Question,
	asked map[int64]bool,
	seen []SeenRoundPhase,
) ExportRoundSlot {
	seenRoundPhases := make(map[seenKey]bool, len(seen))
	for _, sp := range seen {
		seenRoundPhases[seenKey{roundID: sp.RoundID, phase: sp.Phase}] = true
	}

	slot := nextRoundSlot(rounds, questions, asked, seenRoundPhases)

	return ExportRoundSlot{
		Kind:     slot.kind,
		Question: slot.question,
		Round:    slot.round,
		Phase:    slot.phase,
	}
}

// ExportSlotKindQuestion and ExportSlotKindRoundBoundary expose the
// unexported slot-kind discriminators for assertions.
const (
	ExportSlotKindQuestion      = slotKindQuestion
	ExportSlotKindRoundBoundary = slotKindRoundBoundary
)

// ExportIntroBoundaryWindow drives buildRoundBoundaryItem on the intro
// phase, which returns before touching the stores, and returns the
// resulting StartedAt/ExpiredAt so a test can assert the boundary window
// is always positive, even when the quiz default time limit is zero.
func ExportIntroBoundaryWindow(qz *quiz.Quiz) (startedAt, expiredAt time.Time) {
	svc := &Service{}
	item, err := svc.buildRoundBoundaryItem(
		context.Background(), &Game{}, qz, 0, &quiz.Round{}, RoundPhaseIntro,
	)
	if err != nil {
		panic(err)
	}

	return item.StartedAt, item.ExpiredAt
}
