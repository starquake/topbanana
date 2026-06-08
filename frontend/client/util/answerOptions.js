// Shared answer-option styling for the player-client surfaces (the solo game
// and the join / in-game surface). Both render Kahoot-style per-index tones
// during the answer window and a correctness skin at reveal; this keeps one
// source for that mapping instead of a copy per component.

// QUESTION_OPTION_TONES cycles the four answer-button tones over a question's
// options by index.
export const QUESTION_OPTION_TONES = [
    'btn-answer-tone-a',
    'btn-answer-tone-b',
    'btn-answer-tone-c',
    'btn-answer-tone-d',
];

// optionStateClass returns the class string for an answer button. The caller
// supplies the current state:
//   - revealed:      true once the correct answer is shown; the correctness
//                    skin then overrides the tone (correct / wrong / dim).
//   - correctIds:    the revealed correct-option ids (empty before reveal).
//   - pickedId:      the option id the viewer picked, or null.
//   - highlightPick: when true, the viewer's own pick keeps its tone but gains
//                    a filled background + accent ring during the answer window
//                    so an answered/waiting state is legible without leaking
//                    correctness. The solo game leaves this false (it advances
//                    on pick, so there is no waiting state to mark); the live
//                    surface sets it true.
//
// A timed-out / unanswered question has no correctIds, so at reveal every
// option falls through to dim.
export function optionStateClass(option, idx, { revealed = false, correctIds = [], pickedId = null, highlightPick = false } = {}) {
    if (revealed) {
        if (correctIds.includes(option.id)) return 'btn-answer-correct';
        if (pickedId === option.id) return 'btn-answer-wrong';
        return 'btn-answer-dim';
    }
    const tone = QUESTION_OPTION_TONES[idx % QUESTION_OPTION_TONES.length];
    if (highlightPick && pickedId === option.id) return `btn-answer ${tone} bg-surface-2 ring-2 ring-accent`;
    return `btn-answer ${tone}`;
}
