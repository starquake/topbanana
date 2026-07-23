// quiz-editor.js drives the two-pane question editor (#1244): which rail row
// reads as selected, whether the pane has unsaved edits, and the keyboard map
// that makes bulk editing bearable.
//
// Plain ES module rather than an Alpine component: Alpine is not loaded on the
// admin surface at all (only the player client and host big screen pull it in),
// and this needs no reactive template bindings - just listeners.
//
// Everything is delegated from stable parents, because htmx replaces the pane's
// contents on every selection and swaps rail rows out of band on every save.
import { onDomReady } from '@shared/domReady.js';

const RAIL_ROW = 'article.q-row';
const PANE_ID = 'question-editor';

// Set by Save-and-next so the post-save swap knows to advance. Cleared as soon
// as it is consumed, so a plain save never moves the selection.
let advanceAfterSave = false;

function pane() {
    return document.getElementById(PANE_ID);
}

function rows() {
    return Array.from(document.querySelectorAll(RAIL_ROW));
}

function selectedRow() {
    return document.querySelector(`${RAIL_ROW}[aria-current="true"]`);
}

// markSelected moves the aria-current flag, which is both the accessible
// "you are here" and what the stylesheet keys the highlight on.
function markSelected(row) {
    for (const other of rows()) {
        if (other === row) {
            other.setAttribute('aria-current', 'true');
        } else {
            other.removeAttribute('aria-current');
        }
    }
    if (row && typeof row.scrollIntoView === 'function') {
        row.scrollIntoView({ block: 'nearest' });
    }
}

// Below the editor's breakpoint only one pane shows at a time (#1259). The
// flag lives on <body> rather than in the URL alone because an htmx selection
// never reloads the page, so the server-rendered markup cannot track it.
function setPaneOpen(open) {
    if (open) {
        document.body.dataset.paneOpen = '';
    } else {
        delete document.body.dataset.paneOpen;
    }
}

function setDirty(dirty) {
    const label = document.querySelector('[data-editor-savestate]');
    if (label) {
        label.textContent = dirty ? 'Unsaved changes' : 'All changes saved';
        label.dataset.state = dirty ? 'dirty' : 'clean';
    }

    const row = selectedRow();
    if (row) {
        row.toggleAttribute('data-unsaved', dirty);
    }
}

function isDirty() {
    return document.querySelector('[data-editor-savestate]')?.dataset.state === 'dirty';
}

function paneForm() {
    return pane()?.querySelector('form') ?? null;
}

function submitPane() {
    const form = paneForm();
    if (!form) return false;

    // requestSubmit runs validation and fires submit, which htmx is bound to;
    // form.submit() would bypass both and full-page post.
    form.requestSubmit();

    return true;
}

// selectByOffset moves the selection through the rail. Clicking the row reuses
// the same hx-get the mouse path takes, so there is one selection mechanism.
function selectByOffset(delta) {
    const all = rows();
    if (all.length === 0) return;

    const current = selectedRow();
    const index = current ? all.indexOf(current) : -1;
    const next = all[index < 0 ? 0 : index + delta];
    if (next) next.click();
}

function onKeydown(event) {
    const typing = /^(INPUT|TEXTAREA|SELECT)$/.test(document.activeElement?.tagName ?? '');
    const mod = event.ctrlKey || event.metaKey;

    if (mod && event.key.toLowerCase() === 's') {
        event.preventDefault();
        submitPane();

        return;
    }

    if (mod && event.key === 'Enter') {
        event.preventDefault();
        advanceAfterSave = submitPane();

        return;
    }

    if (mod && event.key === '/') {
        event.preventDefault();
        const text = pane()?.querySelector('textarea[name="text"]');
        if (text) {
            text.focus();
            text.setSelectionRange(text.value.length, text.value.length);
        }

        return;
    }

    // Arrows only move the selection when the caret is not in a field;
    // otherwise they would fight normal text navigation.
    if (typing) return;

    if (event.key === 'ArrowDown') {
        event.preventDefault();
        selectByOffset(1);
    } else if (event.key === 'ArrowUp') {
        event.preventDefault();
        selectByOffset(-1);
    }
}

function init() {
    const editorPane = pane();
    if (!editorPane) return;

    // A ?q= deep link renders the pane's hx-trigger="load" fetch; mirror the
    // selection in the rail so the highlight matches what is open.
    const params = new URLSearchParams(window.location.search);
    // A deep link opens straight into the pane on a narrow screen; without a
    // selection the rail is what you land on.
    setPaneOpen(Boolean(params.get('q') || params.get('r')));

    const selected = params.get('q');
    if (selected) {
        const row = document.querySelector(`${RAIL_ROW}[data-question-id="${CSS.escape(selected)}"]`);
        if (row) markSelected(row);
    }

    document.addEventListener('click', (event) => {
        const row = event.target.closest(RAIL_ROW);
        if (row) {
            markSelected(row);
            setPaneOpen(true);
        }
        if (event.target.closest('[data-editor-round-row]')) {
            setPaneOpen(true);
        }
        if (event.target.closest('[data-editor-add-question], [data-editor-add-round]')) {
            setPaneOpen(true);
        }
        if (event.target.closest('[data-editor-back]')) {
            event.preventDefault();
            setPaneOpen(false);
        }
    });

    // Any edit in the pane marks it dirty. Delegated on the pane because htmx
    // replaces its contents wholesale.
    editorPane.addEventListener('input', () => setDirty(true));
    editorPane.addEventListener('change', () => setDirty(true));

    // A swap into the pane is either a freshly loaded form or a saved one;
    // both start clean.
    editorPane.addEventListener('htmx:afterSwap', () => {
        setDirty(false);
        if (advanceAfterSave) {
            advanceAfterSave = false;
            selectByOffset(1);
        }
    });

    document.addEventListener('keydown', onKeydown);

    // hx-push-url puts the selection in history, so Back should return to the
    // rail rather than leaving a stale pane on screen.
    window.addEventListener('popstate', () => {
        const q = new URLSearchParams(window.location.search);
        setPaneOpen(Boolean(q.get('q') || q.get('r')));
    });

    // The Save-and-next button takes the same path as Ctrl+Enter. Delegated on
    // the pane because htmx replaces the form on every selection and save.
    editorPane.addEventListener('click', (event) => {
        if (event.target.closest('[data-editor-save-next]')) {
            event.preventDefault();
            advanceAfterSave = submitPane();
        }
    });
}

onDomReady(init);

export { isDirty };
