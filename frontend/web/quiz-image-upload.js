// Auto-upload module for the quiz library form (#951). When the host picks
// files in the file input we fire one POST per file with Accept:
// application/json so the server replies with a per-file outcome instead of a
// redirect. Each row shows a progress bar driven by XHR's upload events plus a
// cancel button (xhr.abort) so the host can drop a file mid-upload. Once every
// row is settled and at least one file actually landed we reload the page so
// the library grid + delete modals reflect the new entries; without JS the
// form's submit button still posts the old multi-file multipart and gets the
// redirect/banner flow.

(function () {
    const input = document.getElementById('quiz-media-upload');
    if (!input) return;
    const queue = document.querySelector('[data-image-upload-queue]');
    if (!queue) return;
    const form = input.closest('form');
    if (!form) return;
    const submitBtn = form.querySelector('button[type="submit"]');

    // The hidden submit button is the no-JS fallback. With JS we drive the
    // uploads from the change event, so the button has nothing useful to do.
    if (submitBtn) submitBtn.hidden = true;

    let inFlight = 0;
    let landed = 0;
    let skipped = 0;

    input.addEventListener('change', () => {
        const files = Array.from(input.files || []);
        // Reset the input so picking the same file again still fires change.
        input.value = '';
        for (const file of files) {
            startUpload(file);
        }
    });

    function startUpload(file) {
        const row = document.createElement('li');
        row.className = 'flex flex-col gap-1 rounded-sm border border-border-soft bg-surface px-3 py-2 text-sm';
        row.dataset.uploadRow = '';

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

        inFlight++;

        const body = new FormData();
        body.append('images', file);
        const tokenInput = form.querySelector('input[name="csrf_token"]');
        if (tokenInput && tokenInput.value) body.append('csrf_token', tokenInput.value);

        const xhr = new XMLHttpRequest();
        xhr.open('POST', form.action);
        xhr.setRequestHeader('Accept', 'application/json');
        xhr.withCredentials = true;
        xhr.upload.addEventListener('progress', (event) => {
            if (!event.lengthComputable) return;
            const pct = Math.min(100, Math.round((event.loaded / event.total) * 100));
            bar.value = pct;
            status.textContent = pct + '%';
        });
        xhr.upload.addEventListener('load', () => {
            // Upload bytes are in; now the server decodes, re-encodes, and writes
            // to disk. The bar parks at 100 and the status flips to a holding
            // message until the response arrives.
            bar.value = 100;
            status.textContent = 'Processing...';
        });
        xhr.addEventListener('load', () => {
            cancelBtn.remove();
            bar.remove();
            let json = null;
            try {
                json = JSON.parse(xhr.responseText);
            } catch (_err) {
                // fall through with json=null
            }
            if (xhr.status < 200 || xhr.status >= 300 || !json) {
                finishRow(row, status, 'Failed', false);

                return;
            }
            const uploaded = (json.uploaded || []).length > 0;
            const reason = (json.failed && json.failed[0] && json.failed[0].reason) || 'Failed';
            if (uploaded) {
                landed++;
                finishRow(row, status, 'Uploaded', true);
            } else {
                skipped++;
                finishRow(row, status, reason, false);
            }
        });
        xhr.addEventListener('error', () => {
            cancelBtn.remove();
            bar.remove();
            skipped++;
            finishRow(row, status, 'Failed', false);
        });
        xhr.addEventListener('abort', () => {
            cancelBtn.remove();
            bar.remove();
            finishRow(row, status, 'Cancelled', false);
        });
        xhr.addEventListener('loadend', () => {
            inFlight--;
            if (inFlight === 0 && landed > 0) {
                // Mirror the no-JS form-POST redirect URL so the server-side
                // banner + library grid re-render with the new rows AND the
                // browser scrolls back to the library section via #images
                // (a bare reload would land at the page top, regressing #308).
                const params = new URLSearchParams({
                    uploaded: String(landed),
                    failed: String(skipped),
                });
                window.location.href = window.location.pathname + '?' + params + '#images';
            }
        });
        cancelBtn.addEventListener('click', () => xhr.abort());

        xhr.send(body);
    }

    function finishRow(row, status, text, success) {
        status.textContent = text;
        status.classList.remove('text-text-dim');
        status.classList.add(success ? 'text-success' : 'text-text-dim');
        if (!success) row.classList.add('opacity-70');
    }
})();
