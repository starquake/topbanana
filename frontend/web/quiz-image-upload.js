// Auto-upload for the quiz library form (#951). One XHR per file with
// Accept: application/json; on batch settle, navigate to ?uploaded=N&failed=M
// so the server renders the banner + refreshed library. No-JS falls back to
// the form's submit button posting the multipart. The queue/XHR/progress
// machinery is shared with the sound-library upload via @shared/uploadQueue.js.

import { createUploadQueue } from '@shared/uploadQueue.js';
import { onDomReady } from '@shared/domReady.js';

function wireImageUpload() {
    const input = document.getElementById('quiz-media-upload');
    if (!input) return;
    const queue = document.querySelector('[data-image-upload-queue]');
    if (!queue) return;
    const form = input.closest('form');
    if (!form) return;

    // Hide the form's submit button once the JS module is wired up; without
    // this the still-clickable button would trigger HTML5 'required'
    // validation ('Please select a file') after change clears input.value,
    // making the host think the upload broke.
    const submitBtn = form.querySelector('button[type="submit"]');
    if (submitBtn) submitBtn.hidden = true;

    createUploadQueue({
        input,
        queue,
        form,
        fieldName: 'images',
        rowTestId: 'upload-row',
        cancellable: true,
        maxBytes: Number(input.dataset.maxBytes) || 0,
        maxSizeLabel: input.dataset.maxSizeLabel || '',
        isLanded: (json) => Array.isArray(json.uploaded) && json.uploaded.length > 0,
        failureReason: (json) => {
            // Only treat the body as a structured failure when it carries the
            // uploaded/failed arrays; otherwise return '' so the caller falls
            // back to the server's plain-text reason (e.g. a proxy that strips
            // the JSON Content-Type).
            if (!Array.isArray(json.uploaded) && !Array.isArray(json.failed)) return '';

            return (json.failed && json.failed[0] && json.failed[0].reason) || 'Upload failed';
        },
        onSettle: ({ landed, skipped, cancelled }) => {
            // Navigate even on an all-skipped batch so the server renders the banner.
            const params = new URLSearchParams({
                uploaded: String(landed),
                failed: String(skipped),
                cancelled: String(cancelled),
            });
            window.location.href = window.location.pathname + '?' + params + '#images';
        },
    });
}

onDomReady(wireImageUpload);
