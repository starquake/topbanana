// Shared between-rounds standings bar graph for the player join surface and
// the host TV. Both build the same rows from the server standings (held in
// rank order, best-first) and grow each bar from its pre-round total to its new
// total while the numeric label counts up, then rest the rows in rank order.
// esbuild inlines this into each tree's bundle, so there is no cross-tree
// runtime fetch. The reduced-motion / missing-global fallback lives in the
// shared anim.js runAnim wrapper the caller passes in, so a skipped animation
// snaps straight to the final totals.

// STANDINGS_BAR_DURATION is how long each bar spends growing from its pre-round
// total to its new total (ms).
export const STANDINGS_BAR_DURATION = 900;

// buildStandingsRows maps the server standings into the rendered rows and the
// max total used for bar widths. When animate is true each row's displayTotal
// starts at the pre-round total (so the bars grow into the round's points);
// otherwise it lands straight on the final total (the finished phase, where
// roundScore is 0). ownsRow, when supplied, marks the viewer's own row (isMe)
// for highlighting; the host surface omits it.
export function buildStandingsRows(standings, { animate, ownsRow = null } = {}) {
    const maxTotal = Math.max(1, ...standings.map((s) => s.totalScore));
    const rows = standings.map((s) => ({
        playerId: s.playerId,
        displayName: s.displayName,
        rank: s.rank,
        total: s.totalScore,
        preTotal: s.totalScore - s.roundScore,
        isMe: ownsRow ? ownsRow(s) : false,
        displayTotal: animate ? s.totalScore - s.roundScore : s.totalScore,
    }));

    return { rows, maxTotal };
}

// animateStandingsBars grows each row's displayTotal from its pre-round total
// to its new total; callers bind displayTotal to both the bar width and the
// numeric label so the count-up and fill move together. runAnim (the shared
// anim.js wrapper) snaps to the final state via complete under reduced motion
// or a missing anime global, so no bar sticks half-grown. A round with no score
// movement (or a missing anime global) skips straight to the final totals.
export function animateStandingsBars(bars, runAnim, duration = STANDINGS_BAR_DURATION) {
    const anime = typeof window !== 'undefined' ? window.anime : null;
    const hasMovement = bars.some((bar) => bar.total !== bar.preTotal);
    if (!hasMovement || !anime) {
        bars.forEach((bar) => { bar.displayTotal = bar.total; });

        return;
    }
    bars.forEach((bar) => {
        const proxy = { v: bar.preTotal };
        runAnim(proxy, {
            v: bar.total,
            duration,
            easing: 'easeOutCubic',
            update: () => { bar.displayTotal = Math.round(proxy.v); },
            complete: () => { bar.displayTotal = bar.total; },
        });
    });
}
