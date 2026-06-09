// Shared between-rounds standings bar graph for the player join surface and
// the host TV. Both build the same rows from the server standings (held in
// rank order, best-first) and grow each bar from its pre-round total to its new
// total while the numeric label counts up. From the second screen on, the rows
// first render in the previous screen's order and then slide into the new
// best-first order (a FLIP swap, #730), so an overtake reads as two rows
// trading places rather than snapping into order. esbuild inlines this into
// each tree's bundle, so there is no cross-tree runtime fetch. The
// reduced-motion / missing-global fallback lives in the shared anim.js runAnim
// wrapper the caller passes in, so a skipped animation snaps straight to the
// final totals and the final order.

// STANDINGS_BAR_DURATION is how long each bar spends growing from its pre-round
// total to its new total (ms).
export const STANDINGS_BAR_DURATION = 900;

// STANDINGS_FLIP_DURATION is how long a row spends sliding from its old rank
// position to its new one (ms). Shorter than the bar grow so the resort reads
// as a quick swap that settles while the count-up is still running.
export const STANDINGS_FLIP_DURATION = 450;

// buildStandingsRows maps the server standings into the rendered rows and the
// max total used for bar widths. When animate is true each row's displayTotal
// starts at the pre-round total (so the bars grow into the round's points);
// otherwise it lands straight on the final total. Both the round_results and
// finished phases animate: the finished standings carry the last round's
// roundScore so the bars grow into that final contribution. ownsRow, when
// supplied, marks the viewer's own row (isMe) for highlighting; the host
// surface omits it.
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
            ease: 'outCubic',
            onUpdate: () => { bar.displayTotal = Math.round(proxy.v); },
            onComplete: () => { bar.displayTotal = bar.total; },
        });
    });
}

// orderRowsBy returns the rows reordered to match prevOrder (an array of
// playerId strings from the previous standings screen). Standings are cleared
// to empty during the question/reveal phases between rounds, so the keyed rows
// are torn down and there is nothing in the DOM to slide from when the next
// screen mounts. To give the FLIP a starting point, the new rows are first
// rendered in the previous screen's order (so each player remounts where it
// last sat) and only then resorted into the new best-first order, which is the
// slide the FLIP animates. A player with no previous position (a late joiner)
// sorts to the end, keeping the new entrants out of the swap.
export function orderRowsBy(rows, prevOrder) {
    const rank = new Map(prevOrder.map((id, i) => [String(id), i]));
    const fallback = prevOrder.length;

    return [...rows].sort((a, b) => {
        const ai = rank.has(String(a.playerId)) ? rank.get(String(a.playerId)) : fallback;
        const bi = rank.has(String(b.playerId)) ? rank.get(String(b.playerId)) : fallback;

        return ai - bi;
    });
}

// measureStandingsRows records the current vertical position of each rendered
// standings row, keyed by its playerId, so a later resort can slide each row
// from where it was (the FLIP "First" measurement). container is the standings
// <ul>; rows carry data-standings-row + data-player-id. Returns an empty map
// when the container is absent (e.g. the phase has no graph yet).
export function measureStandingsRows(container) {
    const tops = new Map();
    if (!container) return tops;
    container.querySelectorAll('[data-standings-row][data-player-id]').forEach((row) => {
        tops.set(row.getAttribute('data-player-id'), row.getBoundingClientRect().top);
    });

    return tops;
}

// playStandingsFlip slides each row from its old position (captured by
// measureStandingsRows before the rows were reordered) to its new one. It
// measures the new positions, sets a translateY offset so each row visually
// starts back where it was, then animates that offset to zero through runAnim.
// runAnim's reduced-motion / missing-global skip path runs complete
// synchronously, which clears the transform in the same frame, so a
// reduced-motion row snaps to its final position with no residual translateY. A
// row whose position did not change (delta 0) is left untouched.
export function playStandingsFlip(container, prevTops, runAnim, duration = STANDINGS_FLIP_DURATION) {
    if (!container || !prevTops || prevTops.size === 0) return;
    container.querySelectorAll('[data-standings-row][data-player-id]').forEach((row) => {
        const playerId = row.getAttribute('data-player-id');
        const oldTop = prevTops.get(playerId);
        if (oldTop === undefined) return;
        const delta = oldTop - row.getBoundingClientRect().top;
        if (delta === 0) return;
        row.style.transform = `translateY(${delta}px)`;
        runAnim(row, {
            translateY: [delta, 0],
            duration,
            ease: 'inOutQuad',
            onComplete: () => { row.style.transform = ''; },
        });
    });
}

// nextFrame runs cb after the browser has committed a layout for pending DOM
// changes: a reactive update made inside an Alpine $nextTick callback is applied
// during the in-progress flush, so a nested $nextTick fires before that update
// reaches the DOM - only the following animation frame sees it. Falls back to a
// macrotask where requestAnimationFrame is absent (non-browser test runs).
function nextFrame(cb) {
    if (typeof window !== 'undefined' && typeof window.requestAnimationFrame === 'function') {
        window.requestAnimationFrame(() => cb());
        return;
    }
    setTimeout(cb, 16);
}

// applyStandingsFlip drives the full grow + slide for one new standings screen,
// shared verbatim by the player and host components so the two surfaces stay in
// lockstep. The caller supplies its reactive setter (setBars) and the matching
// getter (getBars), a getter for the rendered <ul> (getContainer), an
// after-render scheduler (afterRender, the component's $nextTick), and the
// bar-grow runner.
//
// The bar grow runs against getBars(), not the raw rows: once setBars assigns
// the array Alpine wraps it in a reactive proxy, and only mutations through that
// proxy re-render the bound widths/labels - mutating the raw rows would update
// numbers Alpine never sees.
//
// The standings <ul> stays mounted across the standings phases (x-show, not
// x-if), so its rows are present the moment Alpine renders the new x-for keys -
// no mount poll is needed (#773). The two-stage FLIP then needs two committed
// renders:
//   1. Stage the rows in the previous screen's order and wait one $nextTick (the
//      caller's afterRender) for those keys to render, then measure each row's
//      starting position (the FLIP "First").
//   2. Resort into the new best-first order. This setBars runs inside the
//      $nextTick callback - i.e. during Alpine's flush - so a nested $nextTick
//      would fire before the resort reaches the DOM. Wait one animation frame
//      (nextFrame) instead, by which point the resorted rows are laid out, then
//      slide each row back from its old spot (the FLIP "Last" -> "Play") while
//      the bars grow.
// Without a previous order (the first standings screen, or a non-animating one)
// it lands straight on the new order. animateBars' and the FLIP's reduced-motion
// / missing-global skip snap to the finals, so a reduced-motion viewer sees the
// settled order with no residual transform.
export function applyStandingsFlip({
    rows, prevOrder, animate, runAnim, setBars, getBars, getContainer, afterRender, animateBars,
}) {
    if (!animate || !prevOrder || prevOrder.length === 0) {
        setBars(rows);
        if (animate) animateBars(getBars(), runAnim);

        return;
    }

    setBars(orderRowsBy(rows, prevOrder));
    afterRender(() => {
        const prevTops = measureStandingsRows(getContainer());
        setBars(rows);
        animateBars(getBars(), runAnim);
        nextFrame(() => playStandingsFlip(getContainer(), prevTops, runAnim));
    });
}
