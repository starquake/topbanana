// uploadQueue is the shared auto-upload engine behind the quiz image library
// (#951) and the quiz sound library (#1059) forms. Both pick files from a single
// <input>, POST one XHR per file with Accept: application/json, render a per-file
// progress row, cap concurrency client-side, and on batch settle navigate so the
// server re-renders the refreshed library. No-JS falls back to the form's submit
// button posting the multipart.
//
// The two surfaces differ only in their edges, which the caller supplies via
// createUploadQueue's config: the form field name, an optional cancel
// affordance, an optional per-file preparation step (audio measures the clip
// duration and posts it as duration_ms), how a JSON response is judged a success
// or failure, the row's data-testid, and what to do once the batch settles. The
// queue/XHR/progress/settle machinery itself lives here once.

// Match the server's per-route read deadline so a stalled XHR can't pin
// inFlight forever.
const UPLOAD_TIMEOUT_MS = 5 * 60 * 1000;

// Cap concurrent in-flight uploads so a huge pick (e.g. 500 files) can't spawn
// many simultaneous POSTs and saturate the host's CPU/network. The server
// enforces the real per-host/per-quiz limits (429/409); this is a friendly-
// client bound, with the rest queued and started as slots free. see #988
const MAX_CONCURRENT_UPLOADS = 3;

const ROW_CLASS = 'flex flex-col gap-1 rounded-sm border border-border-soft bg-surface px-3 py-2 text-sm';
const TOP_ROW_CLASS = 'flex items-center gap-3';
const LABEL_CLASS = 'min-w-0 grow truncate text-text';
const STATUS_CLASS = 'shrink-0 text-xs text-text-dim tabular-nums';
const PROGRESS_CLASS = 'progress block h-[3px] w-full appearance-none rounded-full overflow-hidden bg-border-soft [&::-webkit-progress-bar]:bg-border-soft [&::-webkit-progress-value]:transition-[width] [&::-webkit-progress-value]:duration-100 [&::-webkit-progress-value]:ease-linear [&::-webkit-progress-value]:bg-accent [&::-moz-progress-bar]:bg-accent';
const CANCEL_BTN_CLASS = 'inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-full border border-border-soft text-text-dim hover:border-danger hover:text-danger focus-visible:outline-none focus-visible:shadow-focus';
const CANCEL_ICON_HTML =
    '<svg width="10" height="10" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true">'
    + '<path d="M4.646 4.646a.5.5 0 0 1 .708 0L8 7.293l2.646-2.647a.5.5 0 0 1 .708.708L8.707 8l2.647 2.646a.5.5 0 0 1-.708.708L8 8.707l-2.646 2.647a.5.5 0 0 1-.708-.708L7.293 8 4.646 5.354a.5.5 0 0 1 0-.708z"/></svg>';

// readPlainText surfaces the server's plain-text error (e.g. "invalid or
// oversized upload") clipped to a single line of at most 140 chars, so a
// misconfigured proxy that strips the JSON Content-Type still yields a readable
// failure reason instead of a wall of HTML.
function readPlainText(xhr) {
    const body = (xhr.responseText || '').trim();
    if (!body) return '';
    const firstLine = body.split('\n', 1)[0];

    return firstLine.length > 140 ? firstLine.slice(0, 137) + '...' : firstLine;
}

function finishRow(row, status, text, success) {
    status.textContent = text;
    status.classList.toggle('text-success', success);
    status.classList.toggle('text-text-dim', !success);
    if (!success) row.classList.add('opacity-70');
}

// createUploadQueue wires the upload engine to one form. config:
//   - input:        the file <input> element (required).
//   - queue:        the <ul>/<ol> rows are appended to (required).
//   - form:         the <form> whose action + csrf_token the XHR uses (required).
//   - fieldName:    multipart field name for the file ('images' / 'audio').
//   - rowTestId:    data-testid set on each row ('upload-row' / 'audio-upload-row').
//   - cancellable:  when true, each row renders a Cancel button that aborts the
//                   XHR (an aborted file counts toward the batch's cancelled tally).
//   - maxBytes:     optional per-file size cap in bytes. A picked file larger
//                   than this is rejected before its XHR opens (counts as
//                   skipped); zero or absent disables the guard. The server
//                   stays authoritative - this is a client courtesy (#1139).
//   - maxSizeLabel: human-readable form of maxBytes ('10 MB') shown in the
//                   rejection message. Optional; falls back to a generic message.
//   - prepare(file): optional async hook resolving to extra FormData fields,
//                   e.g. { duration_ms: '1234' }. Defaults to no extra fields.
//   - isLanded(json): given a parsed 2xx JSON body, returns true when the file
//                   landed. Required.
//   - failureReason(json): given a parsed 2xx JSON body of a non-landed file,
//                   returns the reason string, or '' to fall back to plain text.
//                   Defaults to ''.
//   - onSettle(batch): called once the whole batch (in-flight + queued) drains,
//                   with { landed, skipped, cancelled }. Required.
export function createUploadQueue(config) {
    const {
        input,
        queue,
        form,
        fieldName,
        rowTestId,
        cancellable = false,
        maxBytes = 0,
        maxSizeLabel = '',
        prepare = () => Promise.resolve(null),
        isLanded,
        failureReason = () => '',
        onSettle,
    } = config;

    const oversizeMessage = maxSizeLabel ? `Too large (max ${maxSizeLabel})` : 'File is too large';

    // Batch-scoped counters so a prior all-fail batch can't leak into the next
    // settle. pending holds files picked but not yet started.
    let batch = null;
    const pending = [];

    function csrfToken() {
        const tokenInput = form.querySelector('input[name="csrf_token"]');

        return tokenInput && tokenInput.value ? tokenInput.value : '';
    }

    // Start queued uploads until the in-flight cap is reached or the queue
    // drains. Called on change and again whenever an upload settles. Rows are
    // created only when an upload actually starts (in startUpload), so at most
    // MAX_CONCURRENT_UPLOADS rows exist at once and queued files render no row;
    // they therefore cannot be individually cancelled before they start, which
    // is acceptable.
    function pump() {
        if (!batch) return;
        while (batch.inFlight < MAX_CONCURRENT_UPLOADS && pending.length > 0) {
            startUpload(pending.shift(), batch);
        }
    }

    // settle accounts one finished upload, pumps the next queued file, and calls
    // onSettle only once the whole batch is done: no in-flight uploads AND no
    // files still queued. Both the normal loadend path and the synchronous-throw
    // fallback route through here, so the settle gate is identical and can't fire
    // while files are still waiting to start.
    function settle(b) {
        b.inFlight--;
        pump();
        // Re-entrancy guard: an all-synchronous-throw batch recurses startUpload
        // -> catch -> settle -> pump -> startUpload..., so the deepest frame
        // settles and nulls batch first; the unwinding outer frames (already
        // past pump) must not run the settle block again.
        if (!batch || b.inFlight > 0 || pending.length > 0) return;
        batch = null;
        onSettle({ landed: b.landed, skipped: b.skipped, cancelled: b.cancelled });
    }

    function startUpload(file, b) {
        const row = document.createElement('li');
        row.className = ROW_CLASS;
        row.dataset.uploadRow = '';
        row.setAttribute('data-testid', rowTestId);

        const topRow = document.createElement('div');
        topRow.className = TOP_ROW_CLASS;
        row.appendChild(topRow);

        const label = document.createElement('span');
        label.className = LABEL_CLASS;
        label.textContent = file.name;
        topRow.appendChild(label);

        const status = document.createElement('span');
        status.className = STATUS_CLASS;
        status.dataset.uploadStatus = '';
        status.textContent = '0%';
        topRow.appendChild(status);

        queue.appendChild(row);
        b.inFlight++;

        // Client-side size guard (#1139): reject an over-cap file before opening
        // its XHR so the browser never uploads bytes the server would reject. The
        // server 4xx stays the real boundary; this is a courtesy. Settle in a
        // microtask (as the valid path does via prepare().then) so the pump loop
        // that started this file finishes before batch accounting unwinds - a
        // synchronous settle here could null the batch mid-loop. A rejected file
        // counts as skipped and frees its slot immediately, so it holds no queue
        // slot while doing nothing.
        if (maxBytes > 0 && file.size > maxBytes) {
            Promise.resolve().then(() => {
                b.skipped++;
                finishRow(row, status, oversizeMessage, false);
                settle(b);
            });

            return;
        }

        let cancelBtn = null;
        if (cancellable) {
            cancelBtn = document.createElement('button');
            cancelBtn.type = 'button';
            cancelBtn.className = CANCEL_BTN_CLASS;
            cancelBtn.setAttribute('aria-label', `Cancel upload of ${file.name}`);
            cancelBtn.innerHTML = CANCEL_ICON_HTML;
            topRow.appendChild(cancelBtn);
        }

        const bar = document.createElement('progress');
        bar.className = PROGRESS_CLASS;
        bar.dataset.uploadProgress = '';
        bar.max = 100;
        bar.value = 0;
        row.appendChild(bar);

        Promise.resolve(prepare(file)).then((extraFields) => {
            sendUpload({ file, b, row, status, bar, cancelBtn, extraFields });
        });
    }

    function sendUpload({ file, b, row, status, bar, cancelBtn, extraFields }) {
        const removeCancel = () => {
            if (cancelBtn && cancelBtn.parentNode) cancelBtn.remove();
        };

        const body = new FormData();
        body.append(fieldName, file);
        if (extraFields) {
            for (const [key, value] of Object.entries(extraFields)) {
                body.append(key, value);
            }
        }
        const token = csrfToken();
        if (token) body.append('csrf_token', token);

        const xhr = new XMLHttpRequest();
        xhr.open('POST', form.action);
        xhr.setRequestHeader('Accept', 'application/json');
        xhr.timeout = UPLOAD_TIMEOUT_MS;
        xhr.withCredentials = true;
        xhr.upload.addEventListener('progress', (event) => {
            if (!event.lengthComputable) return;
            const pct = Math.min(100, Math.round((event.loaded / event.total) * 100));
            bar.value = pct;
            status.textContent = pct + '%';
            // Pull the cancel affordance as soon as bytes are visibly all in, not
            // on the upload.load event - upload.load fires on the next tick after
            // progress=100, and a Cancel click in that micro-window racing past a
            // server commit produces a 'Cancelled' banner for a file that
            // actually landed in the library.
            if (pct >= 100) removeCancel();
        });
        xhr.upload.addEventListener('load', () => {
            // Defensive: in case 'progress' never fired (event.lengthComputable
            // was false), drop the cancel affordance here too.
            bar.value = 100;
            status.textContent = 'Processing...';
            removeCancel();
        });
        xhr.addEventListener('load', () => {
            removeCancel();
            bar.remove();
            handleResponse(xhr, row, status, b);
        });
        xhr.addEventListener('error', () => {
            removeCancel();
            bar.remove();
            b.skipped++;
            finishRow(row, status, 'Upload failed', false);
        });
        xhr.addEventListener('timeout', () => {
            removeCancel();
            bar.remove();
            b.skipped++;
            finishRow(row, status, 'Upload timed out', false);
        });
        xhr.addEventListener('abort', () => {
            removeCancel();
            bar.remove();
            b.cancelled++;
            finishRow(row, status, 'Cancelled', false);
        });
        xhr.addEventListener('loadend', () => settle(b));
        if (cancelBtn) cancelBtn.addEventListener('click', () => xhr.abort());

        // A synchronous throw from xhr.send (CSP block, browser security
        // exception) would leak inFlight; fall through to the error path so the
        // batch can still settle and navigate.
        try {
            xhr.send(body);
        } catch (_err) {
            removeCancel();
            bar.remove();
            b.skipped++;
            finishRow(row, status, 'Upload failed', false);
            // A synchronous throw before send bypasses the loadend event, so
            // settle explicitly here to keep the batch accounting accurate and
            // pump the next queued file.
            settle(b);
        }
    }

    function handleResponse(xhr, row, status, b) {
        // Try to parse JSON regardless of Content-Type. A misconfigured proxy
        // that strips Content-Type would otherwise force a successful upload into
        // the plain-text fallback and the row gets counted as failed.
        let json = null;
        if (xhr.status >= 200 && xhr.status < 300) {
            try { json = JSON.parse(xhr.responseText); } catch (_err) { /* json stays null */ }
        }
        if (json) {
            if (isLanded(json)) {
                b.landed++;
                finishRow(row, status, 'Uploaded', true);

                return;
            }
            const reason = failureReason(json);
            if (reason) {
                b.skipped++;
                finishRow(row, status, reason, false);

                return;
            }
        }
        const fallback = readPlainText(xhr) || 'Upload failed';
        b.skipped++;
        finishRow(row, status, fallback, false);
    }

    input.addEventListener('change', () => {
        const files = Array.from(input.files || []);
        input.value = ''; // re-pick of the same file should still fire change
        if (files.length === 0) return;
        if (!batch) batch = { inFlight: 0, landed: 0, skipped: 0, cancelled: 0 };
        for (const file of files) pending.push(file);
        pump();
    });
}
