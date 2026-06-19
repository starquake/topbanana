// Auto-upload for the quiz sound library form (#1059). One XHR per file with
// Accept: application/json; the clip duration is measured in the browser and
// posted as duration_ms alongside the file. On batch settle the page reloads
// to the sounds section so the new clips appear. No-JS falls back to the form's
// submit button posting the multipart (without a measured duration).

(function () {
    const input = document.getElementById('quiz-audio-upload');
    if (!input) return;
    const queue = document.querySelector('[data-audio-upload-queue]');
    if (!queue) return;
    const form = input.closest('form');
    if (!form) return;
    const submitBtn = form.querySelector('button[type="submit"]');

    // Hide the form's submit button once the JS module is wired up; without
    // this the still-clickable button would trigger HTML5 'required'
    // validation after change clears input.value, making the host think the
    // upload broke.
    if (submitBtn) submitBtn.hidden = true;

    // Match the server's per-route read deadline so a stalled XHR can't pin
    // inFlight forever.
    const UPLOAD_TIMEOUT_MS = 5 * 60 * 1000;

    // How long to wait for the browser to read a clip's duration before giving
    // up and uploading without it (duration is advisory; the server stores NULL
    // when it is absent).
    const DURATION_TIMEOUT_MS = 5000;

    // Cap concurrent in-flight uploads so a huge pick can't spawn many
    // simultaneous POSTs. The server enforces the real per-host/per-quiz limits
    // (429/409); this is a friendly-client bound, see #988.
    const MAX_CONCURRENT_UPLOADS = 3;

    let batch = null;
    const pending = [];

    input.addEventListener('change', () => {
        const files = Array.from(input.files || []);
        input.value = ''; // re-pick of the same file should still fire change
        if (files.length === 0) return;
        if (!batch) batch = { inFlight: 0, landed: 0, skipped: 0 };
        for (const file of files) pending.push(file);
        pump();
    });

    // Start queued uploads until the in-flight cap is reached or the queue
    // drains. Rows are created only when an upload actually starts, so at most
    // MAX_CONCURRENT_UPLOADS rows exist at once.
    function pump() {
        if (!batch) return;
        while (batch.inFlight < MAX_CONCURRENT_UPLOADS && pending.length > 0) {
            startUpload(pending.shift(), batch);
        }
    }

    // settle accounts one finished upload, pumps the next queued file, and
    // reloads only once the whole batch is done: no in-flight uploads and no
    // files still queued. A reload re-renders the sounds list server-side, so no
    // banner query is needed.
    function settle(b) {
        b.inFlight--;
        pump();
        if (!batch || b.inFlight > 0 || pending.length > 0) return;
        batch = null;
        // Only reload when at least one clip actually landed; an all-fail batch
        // leaves the failure rows visible instead of wiping them. Set the hash
        // first (a same-document change, no navigation) so the reload lands on
        // the sounds section, then reload to pull in the new rows.
        if (b.landed > 0) {
            window.location.hash = 'sounds';
            window.location.reload();
        }
    }

    // measureDuration resolves to the clip length in whole milliseconds, or 0
    // when it cannot be read in time. The object URL is revoked once metadata
    // has loaded (or the wait times out) so a picked file is not retained.
    function measureDuration(file) {
        return new Promise((resolve) => {
            const url = URL.createObjectURL(file);
            const probe = new Audio();
            let done = false;
            const finish = (ms) => {
                if (done) return;
                done = true;
                URL.revokeObjectURL(url);
                resolve(ms);
            };
            const timer = setTimeout(() => finish(0), DURATION_TIMEOUT_MS);
            probe.addEventListener('loadedmetadata', () => {
                clearTimeout(timer);
                const seconds = probe.duration;
                const ms = Number.isFinite(seconds) && seconds > 0 ? Math.round(seconds * 1000) : 0;
                finish(ms);
            });
            probe.addEventListener('error', () => {
                clearTimeout(timer);
                finish(0);
            });
            probe.preload = 'metadata';
            probe.src = url;
        });
    }

    function startUpload(file, b) {
        const row = document.createElement('li');
        row.className = 'flex flex-col gap-1 rounded-sm border border-border-soft bg-surface px-3 py-2 text-sm';
        row.setAttribute('data-testid', 'audio-upload-row');

        const topRow = document.createElement('div');
        topRow.className = 'flex items-center gap-3';
        row.appendChild(topRow);

        const label = document.createElement('span');
        label.className = 'min-w-0 grow truncate text-text';
        label.textContent = file.name;
        topRow.appendChild(label);

        const status = document.createElement('span');
        status.className = 'shrink-0 text-xs text-text-dim tabular-nums';
        status.textContent = '0%';
        topRow.appendChild(status);

        const bar = document.createElement('progress');
        bar.className = 'progress block h-[3px] w-full appearance-none rounded-full overflow-hidden bg-border-soft [&::-webkit-progress-bar]:bg-border-soft [&::-webkit-progress-value]:transition-[width] [&::-webkit-progress-value]:duration-100 [&::-webkit-progress-value]:ease-linear [&::-webkit-progress-value]:bg-accent [&::-moz-progress-bar]:bg-accent';
        bar.max = 100;
        bar.value = 0;
        row.appendChild(bar);

        queue.appendChild(row);

        b.inFlight++;

        measureDuration(file).then((durationMs) => sendUpload(file, b, row, status, bar, durationMs));
    }

    function sendUpload(file, b, row, status, bar, durationMs) {
        const body = new FormData();
        body.append('audio', file);
        if (durationMs > 0) body.append('duration_ms', String(durationMs));
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
        });
        xhr.upload.addEventListener('load', () => {
            bar.value = 100;
            status.textContent = 'Processing...';
        });
        xhr.addEventListener('load', () => {
            bar.remove();
            handleResponse(xhr, row, status, b);
        });
        xhr.addEventListener('error', () => {
            bar.remove();
            b.skipped++;
            finishRow(row, status, 'Upload failed', false);
        });
        xhr.addEventListener('timeout', () => {
            bar.remove();
            b.skipped++;
            finishRow(row, status, 'Upload timed out', false);
        });
        xhr.addEventListener('loadend', () => settle(b));

        try {
            xhr.send(body);
        } catch (_err) {
            bar.remove();
            b.skipped++;
            finishRow(row, status, 'Upload failed', false);
            settle(b);
        }
    }

    function handleResponse(xhr, row, status, b) {
        let json = null;
        if (xhr.status >= 200 && xhr.status < 300) {
            try { json = JSON.parse(xhr.responseText); } catch (_err) { /* json stays null */ }
        }
        if (json && typeof json.id === 'number' && json.id > 0) {
            b.landed++;
            finishRow(row, status, 'Uploaded', true);

            return;
        }
        const fallback = readPlainText(xhr) || 'Upload failed';
        b.skipped++;
        finishRow(row, status, fallback, false);
    }

    function readPlainText(xhr) {
        const text = (xhr.responseText || '').trim();
        if (!text) return '';
        const firstLine = text.split('\n', 1)[0];

        return firstLine.length > 140 ? firstLine.slice(0, 137) + '...' : firstLine;
    }

    function finishRow(row, status, text, success) {
        status.textContent = text;
        status.classList.toggle('text-success', success);
        status.classList.toggle('text-text-dim', !success);
        if (!success) row.classList.add('opacity-70');
    }
})();
