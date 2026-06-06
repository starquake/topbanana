// quiz-reorder.js powers reordering of rounds and questions on the admin
// quiz view. The grip handle is the single reorder affordance (#731): drag
// it with a mouse (SortableJS native HTML5 DnD, #199), or focus it and press
// ArrowUp/ArrowDown for the keyboard path. The Up/Down buttons and the
// move-to-round <select> that used to be the keyboard/touch fallback are
// gone, so the keyboard handler here is the a11y reorder path, not an extra.
// The drag affordances (the grip handles and the data-* hooks this module
// reads) only render for an owner/editor, so this module self-noops for a
// read-only viewer.
//
// SortableJS is loaded as a classic script just before this module, exposing
// the global Sortable. Both drag and keyboard POST to the same form-encoded
// /position endpoints and swap in the re-rendered #questions-list partial the
// server returns, so the authoritative order + renumbered positions come from
// the server, not the client guess. A failed POST snaps the list back to its
// pre-move HTML and surfaces a small banner.

const QUESTIONS_LIST_ID = 'questions-list';

const reducedMotion =
    typeof window !== 'undefined' &&
    typeof window.matchMedia === 'function' &&
    window.matchMedia('(prefers-reduced-motion: reduce)').matches;

const ANIMATION_MS = reducedMotion ? 0 : 150;

// Live Sortable instances, destroyed and rebuilt on every swap so they bind
// to the fresh DOM the server returns.
let instances = [];

// preDragHTML is the #questions-list markup captured at drag start, BEFORE
// SortableJS moves the element. onEnd fires after the move, so a post-move
// snapshot would not revert anything; capturing in onStart is what lets a
// failed POST actually snap the item back to where it was.
let preDragHTML = '';

function captureSnapshot() {
    const root = document.getElementById(QUESTIONS_LIST_ID);
    preDragHTML = root ? root.outerHTML : '';
}

function csrfToken(root) {
    return root.dataset.csrf || '';
}

function destroyInstances() {
    for (const inst of instances) {
        inst.destroy();
    }
    instances = [];
}

function showError(root, message) {
    let banner = root.querySelector('[data-reorder-error]');
    if (!banner) {
        banner = document.createElement('div');
        banner.setAttribute('data-reorder-error', '');
        banner.setAttribute('role', 'alert');
        banner.className = 'reorder-error';
        root.prepend(banner);
    }
    banner.textContent = message;
    clearTimeout(showError.timer);
    showError.timer = setTimeout(() => banner.remove(), 4000);
}

// postReorder sends the form-encoded body and, on a 2xx, replaces
// #questions-list with the returned partial and re-initialises. On any
// failure it restores the pre-drop snapshot. Either way Sortable is rebuilt
// against the resulting DOM.
async function postReorder(url, body, snapshotHTML) {
    try {
        const response = await fetch(url, {
            method: 'POST',
            headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
            body,
        });
        if (!response.ok) {
            throw new Error(`reorder failed: ${response.status}`);
        }
        const html = await response.text();
        replaceList(html);
    } catch {
        restoreSnapshot(snapshotHTML);
        const fresh = document.getElementById(QUESTIONS_LIST_ID);
        if (fresh) {
            showError(fresh, 'Could not save the new order. Please try again.');
        }
    }
}

// pendingFocus remembers which handle the keyboard user was on so focus can
// follow the moved item across the server swap; without it, every arrow press
// would drop focus back to the page and stall keyboard reordering after one
// move. Null for drag (the mouse user is not driving from the keyboard).
let pendingFocus = null;

function swapProcessed(newRoot) {
    if (window.htmx && typeof window.htmx.process === 'function') {
        window.htmx.process(newRoot);
    }
    initSortable(newRoot);
    restoreFocus(newRoot);
}

function restoreFocus(root) {
    if (!pendingFocus) return;
    const { type, id } = pendingFocus;
    pendingFocus = null;
    const selector =
        type === 'round'
            ? `section.round-section[data-round-id="${id}"] [data-round-handle]`
            : `article.q-row[data-question-id="${id}"] [data-question-handle]`;
    const handle = root.querySelector(selector);
    if (handle) handle.focus();
}

function replaceList(html) {
    const current = document.getElementById(QUESTIONS_LIST_ID);
    if (!current) return;
    destroyInstances();
    const template = document.createElement('template');
    template.innerHTML = html.trim();
    const next = template.content.querySelector(`#${QUESTIONS_LIST_ID}`);
    if (!next) return;
    current.replaceWith(next);
    swapProcessed(next);
}

function restoreSnapshot(snapshotHTML) {
    const current = document.getElementById(QUESTIONS_LIST_ID);
    if (!current) return;
    destroyInstances();
    current.outerHTML = snapshotHTML;
    const restored = document.getElementById(QUESTIONS_LIST_ID);
    if (restored) {
        swapProcessed(restored);
    }
}

function onRoundEnd(evt) {
    const root = document.getElementById(QUESTIONS_LIST_ID);
    if (!root) return;
    const section = evt.item;
    const roundId = section.dataset.roundId;
    const quizId = root.dataset.quizId;
    const sections = Array.from(root.querySelectorAll('section.round-section'));
    const newPosition = sections.indexOf(section) + 1;
    if (newPosition < 1 || !roundId || !quizId) return;

    const snapshot = preDragHTML || root.outerHTML;
    const body = new URLSearchParams({
        csrf_token: csrfToken(root),
        new_position: String(newPosition),
    });
    postReorder(
        `/admin/quizzes/${quizId}/rounds/${roundId}/position`,
        body,
        snapshot,
    );
}

function onQuestionEnd(evt) {
    const root = document.getElementById(QUESTIONS_LIST_ID);
    if (!root) return;
    const article = evt.item;
    const questionId = article.dataset.questionId;
    const quizId = root.dataset.quizId;
    const targetSection = article.closest('section.round-section');
    if (!targetSection || !questionId || !quizId) return;
    const targetRoundId = targetSection.dataset.roundId;
    const list = targetSection.querySelector('[data-question-list]');
    const items = list ? Array.from(list.querySelectorAll('article.q-row')) : [];
    const newPosition = items.indexOf(article) + 1;
    if (newPosition < 1 || !targetRoundId) return;

    const snapshot = preDragHTML || root.outerHTML;
    const body = new URLSearchParams({
        csrf_token: csrfToken(root),
        round_id: targetRoundId,
        new_position: String(newPosition),
    });
    postReorder(
        `/admin/quizzes/${quizId}/questions/${questionId}/position`,
        body,
        snapshot,
    );
}

// moveRoundByKeyboard reorders a round one slot in `direction` (-1 up, +1
// down) and POSTs the new 1-based position. A no-op at the list edges.
function moveRoundByKeyboard(root, handle, direction) {
    const section = handle.closest('section.round-section');
    if (!section) return;
    const roundId = section.dataset.roundId;
    const quizId = root.dataset.quizId;
    const sections = Array.from(root.querySelectorAll('section.round-section'));
    const index = sections.indexOf(section);
    const newPosition = index + 1 + direction;
    if (newPosition < 1 || newPosition > sections.length || !roundId || !quizId) return;

    pendingFocus = { type: 'round', id: roundId };
    const body = new URLSearchParams({
        csrf_token: csrfToken(root),
        new_position: String(newPosition),
    });
    postReorder(
        `/admin/quizzes/${quizId}/rounds/${roundId}/position`,
        body,
        root.outerHTML,
    );
}

// moveQuestionByKeyboard reorders a question one slot in `direction` within
// its own round and POSTs the new 1-based position. A no-op at the round's
// edges; cross-round moves stay drag-only.
function moveQuestionByKeyboard(root, handle, direction) {
    const article = handle.closest('article.q-row');
    if (!article) return;
    const questionId = article.dataset.questionId;
    const quizId = root.dataset.quizId;
    const section = article.closest('section.round-section');
    if (!section || !questionId || !quizId) return;
    const roundId = section.dataset.roundId;
    const list = section.querySelector('[data-question-list]');
    const items = list ? Array.from(list.querySelectorAll('article.q-row')) : [];
    const index = items.indexOf(article);
    const newPosition = index + 1 + direction;
    if (newPosition < 1 || newPosition > items.length || !roundId) return;

    pendingFocus = { type: 'question', id: questionId };
    const body = new URLSearchParams({
        csrf_token: csrfToken(root),
        round_id: roundId,
        new_position: String(newPosition),
    });
    postReorder(
        `/admin/quizzes/${quizId}/questions/${questionId}/position`,
        body,
        root.outerHTML,
    );
}

function onHandleKeydown(evt) {
    if (evt.key !== 'ArrowUp' && evt.key !== 'ArrowDown') return;
    const root = document.getElementById(QUESTIONS_LIST_ID);
    if (!root) return;
    const direction = evt.key === 'ArrowDown' ? 1 : -1;
    const roundHandle = evt.target.closest('[data-round-handle]');
    if (roundHandle) {
        evt.preventDefault();
        moveRoundByKeyboard(root, roundHandle, direction);

        return;
    }
    const questionHandle = evt.target.closest('[data-question-handle]');
    if (questionHandle) {
        evt.preventDefault();
        moveQuestionByKeyboard(root, questionHandle, direction);
    }
}

function initSortable(root) {
    // Edit handles only render for an owner/editor; their absence means a
    // read-only viewer, so there is nothing to wire.
    if (!root.querySelector('[data-round-handle]')) return;

    // The keyboard path is wired even when SortableJS failed to load, so it is
    // attached before the early return below. Delegated on the swapped-in root,
    // so it survives every partial swap without per-handle rebinding.
    root.addEventListener('keydown', onHandleKeydown);

    if (typeof window.Sortable !== 'function') return;

    const shared = { name: 'questions' };

    // SortableJS runs in native HTML5 drag-and-drop mode, which fires for mouse
    // input only and does NOT engage on touchscreens (#199 keeps touch drag out
    // of scope). Touch and keyboard users reorder via the grip handle's
    // ArrowUp/ArrowDown keys (#731), so the rail stays visible at every
    // breakpoint.
    const common = {
        animation: ANIMATION_MS,
        onStart: captureSnapshot,
        ghostClass: 'sortable-ghost',
        chosenClass: 'sortable-chosen',
        dragClass: 'sortable-drag',
    };

    instances.push(
        window.Sortable.create(root, {
            ...common,
            handle: '[data-round-handle]',
            draggable: 'section.round-section',
            onEnd: onRoundEnd,
        }),
    );

    for (const list of root.querySelectorAll('[data-question-list]')) {
        instances.push(
            window.Sortable.create(list, {
                ...common,
                group: shared,
                handle: '[data-question-handle]',
                draggable: 'article.q-row',
                onEnd: onQuestionEnd,
            }),
        );
    }
}

if (typeof document !== 'undefined') {
    const run = () => {
        const root = document.getElementById(QUESTIONS_LIST_ID);
        if (root) initSortable(root);
    };
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', run);
    } else {
        run();
    }
}
