// Auto-upload for the quiz library form (#951). One XHR per file with
// Accept: application/json; on batch settle, navigate to ?uploaded=N&failed=M
// so the server renders the banner + refreshed library. No-JS falls back to
// the form's submit button posting the multipart.

(function () {
    const input = document.getElementById('quiz-media-upload');
    if (!input) return;
    const queue = document.querySelector('[data-image-upload-queue]');
    if (!queue) return;
    const form = input.closest('form');
    if (!form) return;
    const submitBtn = form.querySelector('button[type="submit"]');

    // Hide the form's submit button once the JS module is wired up; without
    // this the still-clickable button would trigger HTML5 'required'
    // validation ('Please select a file') after change clears input.value,
    // making the host think the upload broke.
    if (submitBtn) submitBtn.hidden = true;

    // Match the server's per-route read deadline so a stalled XHR can't pin
    // inFlight forever.
    const UPLOAD_TIMEOUT_MS = 5 * 60 * 1000;

    // Cap concurrent in-flight uploads so a huge pick (e.g. 500 files) can't
    // spawn 500 simultaneous POSTs and saturate the host's CPU/network. The
    // server enforces the real per-host/per-quiz limits (429/409); this is a
    // friendly-client bound, with the rest queued and started as slots free.
    // see #988
    const MAX_CONCURRENT_UPLOADS = 3;

    // Batch-scoped counters so a prior all-fail batch can't leak into the
    // next navigate URL. pending holds files picked but not yet started.
    let batch = null;
    const pending = [];

    input.addEventListener('change', () => {
        const files = Array.from(input.files || []);
        input.value = ''; // re-pick of the same file should still fire change
        if (files.length === 0) return;
        if (!batch) batch = { inFlight: 0, landed: 0, skipped: 0, cancelled: 0 };
        for (const file of files) pending.push(file);
        pump();
    });

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

    // settle accounts one finished upload, pumps the next queued file, and
    // navigates only once the whole batch is done: no in-flight uploads AND no
    // files still queued. Both the normal loadend path and the synchronous-throw
    // fallback route through here, so the navigate gate is identical and can't
    // fire while files are still waiting to start.
    function settle(b) {
        b.inFlight--;
        pump();
        // Re-entrancy guard: an all-synchronous-throw batch recurses
        // startUpload -> catch -> settle -> pump -> startUpload..., so the
        // deepest frame navigates and nulls batch first; the unwinding outer
        // frames (already past pump) must not run the navigate block again.
        if (!batch || b.inFlight > 0 || pending.length > 0) return;
        // Navigate even on an all-skipped batch so the server renders the banner.
        const params = new URLSearchParams({
            uploaded: String(b.landed),
            failed: String(b.skipped),
            cancelled: String(b.cancelled),
        });
        const target = window.location.pathname + '?' + params + '#images';
        batch = null;
        window.location.href = target;
    }

    function startUpload(file, b) {
        const row = document.createElement('li');
        row.className = 'flex flex-col gap-1 rounded-sm border border-border-soft bg-surface px-3 py-2 text-sm';
        row.dataset.uploadRow = '';
        row.setAttribute('data-testid', 'upload-row');

        const topRow = document.createElement('div');
        topRow.className = 'flex items-center gap-3';
        row.appendChild(topRow);

        const label = document.createElement('span');
        label.className = 'min-w-0 grow truncate text-text';
        label.textContent = file.name;
        topRow.appendChild(label);

        const status = document.createElement('span');
        status.className = 'shrink-0 text-xs text-text-dim tabular-nums';
        status.dataset.uploadStatus = '';
        status.textContent = '0%';
        topRow.appendChild(status);

        const cancelBtn = document.createElement('button');
        cancelBtn.type = 'button';
        cancelBtn.className = 'inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-full border border-border-soft text-text-dim hover:border-danger hover:text-danger focus-visible:outline-none focus-visible:shadow-focus';
        cancelBtn.setAttribute('aria-label', `Cancel upload of ${file.name}`);
        cancelBtn.innerHTML =
            '<svg width="10" height="10" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true">'
            + '<path d="M4.646 4.646a.5.5 0 0 1 .708 0L8 7.293l2.646-2.647a.5.5 0 0 1 .708.708L8.707 8l2.647 2.646a.5.5 0 0 1-.708.708L8 8.707l-2.646 2.647a.5.5 0 0 1-.708-.708L7.293 8 4.646 5.354a.5.5 0 0 1 0-.708z"/></svg>';
        topRow.appendChild(cancelBtn);

        const bar = document.createElement('progress');
        bar.className = 'progress block h-[3px] w-full appearance-none rounded-full overflow-hidden bg-border-soft [&::-webkit-progress-bar]:bg-border-soft [&::-webkit-progress-value]:transition-[width] [&::-webkit-progress-value]:duration-100 [&::-webkit-progress-value]:ease-linear [&::-webkit-progress-value]:bg-accent [&::-moz-progress-bar]:bg-accent';
        bar.dataset.uploadProgress = '';
        bar.max = 100;
        bar.value = 0;
        row.appendChild(bar);

        queue.appendChild(row);

        b.inFlight++;

        const body = new FormData();
        body.append('images', file);
        const tokenInput = form.querySelector('input[name="csrf_token"]');
        if (tokenInput && tokenInput.value) body.append('csrf_token', tokenInput.value);

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
            // Pull the cancel affordance as soon as bytes are visibly all in,
            // not on the upload.load event - upload.load fires on the next
            // tick after progress=100, and a Cancel click in that micro-window
            // racing past a server commit produces a 'Cancelled' banner for a
            // file that actually landed in the library.
            if (pct >= 100 && cancelBtn.parentNode) cancelBtn.remove();
        });
        xhr.upload.addEventListener('load', () => {
            // Defensive: in case 'progress' never fired (event.lengthComputable
            // was false), drop the cancel affordance here too.
            bar.value = 100;
            status.textContent = 'Processing...';
            if (cancelBtn.parentNode) cancelBtn.remove();
        });
        xhr.addEventListener('load', () => {
            if (cancelBtn.parentNode) cancelBtn.remove();
            bar.remove();
            handleResponse(xhr, row, status, b);
        });
        xhr.addEventListener('error', () => {
            if (cancelBtn.parentNode) cancelBtn.remove();
            bar.remove();
            b.skipped++;
            finishRow(row, status, 'Upload failed', false);
        });
        xhr.addEventListener('timeout', () => {
            if (cancelBtn.parentNode) cancelBtn.remove();
            bar.remove();
            b.skipped++;
            finishRow(row, status, 'Upload timed out', false);
        });
        xhr.addEventListener('abort', () => {
            if (cancelBtn.parentNode) cancelBtn.remove();
            bar.remove();
            b.cancelled++;
            finishRow(row, status, 'Cancelled', false);
        });
        xhr.addEventListener('loadend', () => {
            settle(b);
        });
        cancelBtn.addEventListener('click', () => xhr.abort());

        // A synchronous throw from xhr.send (CSP block, browser security
        // exception) would leak inFlight; fall through to the error path so
        // the batch can still settle and navigate.
        try {
            xhr.send(body);
        } catch (_err) {
            if (cancelBtn.parentNode) cancelBtn.remove();
            bar.remove();
            b.skipped++;
            finishRow(row, status, 'Upload failed', false);
            // A synchronous throw before send bypasses the loadend event, so
            // settle explicitly here to keep the batch accounting accurate and
            // pump the next queued file. settle gates the navigate on both
            // inFlight === 0 and the queue being empty, same as the loadend path.
            settle(b);
        }
    }

    function handleResponse(xhr, row, status, b) {
        // Try to parse JSON regardless of Content-Type. A misconfigured proxy
        // that strips Content-Type would otherwise force a successful upload
        // into the plain-text fallback and the row gets counted as failed.
        let json = null;
        if (xhr.status >= 200 && xhr.status < 300) {
            try { json = JSON.parse(xhr.responseText); } catch (_err) { /* json stays null */ }
        }
        if (json && (Array.isArray(json.uploaded) || Array.isArray(json.failed))) {
            const uploaded = (json.uploaded || []).length > 0;
            if (uploaded) {
                b.landed++;
                finishRow(row, status, 'Uploaded', true);

                return;
            }
            const reason = (json.failed && json.failed[0] && json.failed[0].reason) || 'Upload failed';
            b.skipped++;
            finishRow(row, status, reason, false);

            return;
        }
        // Surface the server's plain-text message (e.g. "invalid or oversized upload").
        const fallback = readPlainText(xhr) || 'Upload failed';
        b.skipped++;
        finishRow(row, status, fallback, false);
    }

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
})();
