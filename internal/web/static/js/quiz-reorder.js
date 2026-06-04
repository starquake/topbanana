// quiz-reorder.js powers drag-and-drop reordering of rounds and questions
// on the admin quiz view (#199), layered on top of the existing Up/Down
// buttons and the move-to-round <select>, which stay as the no-JS / touch /
// a11y fallback. The drag affordances (the grip handles and the data-*
// hooks this module reads) only render for an owner/editor, so this module
// self-noops for a read-only viewer.
//
// SortableJS is loaded as a classic script just before this module, exposing
// the global Sortable. Each drop POSTs to the same form-encoded reorder
// endpoints the fallback buttons hit and swaps in the re-rendered
// #questions-list partial the server returns, so the authoritative order +
// renumbered positions come from the server, not the client guess. A failed
// POST snaps the list back to its pre-drop HTML and surfaces a small banner.

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

function swapProcessed(newRoot) {
    if (window.htmx && typeof window.htmx.process === 'function') {
        window.htmx.process(newRoot);
    }
    initSortable(newRoot);
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

function initSortable(root) {
    if (typeof window.Sortable !== 'function') return;
    // Edit handles only render for an owner/editor; their absence means a
    // read-only viewer, so there is nothing to wire.
    if (!root.querySelector('[data-round-handle]')) return;

    const shared = { name: 'questions' };

    // SortableJS uses native HTML5 drag-and-drop for mouse input and its own
    // touch-event handling for touch devices (long-press to grab), so the
    // drag affordance covers both pointer types (#199). Keyboard users and
    // anyone without a pointer reorder via the always-present Up/Down buttons
    // + move-to-round <select>, which stay in the markup as the fallback.
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
