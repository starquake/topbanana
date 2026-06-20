// Auto-upload for the quiz sound library form (#1059). One XHR per file with
// Accept: application/json; the clip duration is measured in the browser and
// posted as duration_ms alongside the file. On batch settle the page reloads
// to the sounds section so the new clips appear. No-JS falls back to the form's
// submit button posting the multipart (without a measured duration). The
// queue/XHR/progress machinery is shared with the image-library upload via
// @shared/uploadQueue.js.

import { createUploadQueue } from '@shared/uploadQueue.js';
import { onDomReady } from '@shared/domReady.js';

// How long to wait for the browser to read a clip's duration before giving up
// and uploading without it (duration is advisory; the server stores NULL when
// it is absent).
const DURATION_TIMEOUT_MS = 5000;

// measureDuration resolves to the clip length in whole milliseconds, or 0 when
// it cannot be read in time. The object URL is revoked once metadata has loaded
// (or the wait times out) so a picked file is not retained.
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

function wireAudioUpload() {
    const input = document.getElementById('quiz-audio-upload');
    if (!input) return;
    const queue = document.querySelector('[data-audio-upload-queue]');
    if (!queue) return;
    const form = input.closest('form');
    if (!form) return;

    // Hide the form's submit button once the JS module is wired up; without
    // this the still-clickable button would trigger HTML5 'required' validation
    // after change clears input.value, making the host think the upload broke.
    const submitBtn = form.querySelector('button[type="submit"]');
    if (submitBtn) submitBtn.hidden = true;

    createUploadQueue({
        input,
        queue,
        form,
        fieldName: 'audio',
        rowTestId: 'audio-upload-row',
        prepare: async (file) => {
            const durationMs = await measureDuration(file);

            return durationMs > 0 ? { duration_ms: String(durationMs) } : null;
        },
        isLanded: (json) => typeof json.id === 'number' && json.id > 0,
        onSettle: ({ landed }) => {
            // Only reload when at least one clip actually landed; an all-fail
            // batch leaves the failure rows visible instead of wiping them. Set
            // the hash first (a same-document change, no navigation) so the
            // reload lands on the sounds section, then reload to pull in the new
            // rows.
            if (landed > 0) {
                window.location.hash = 'sounds';
                window.location.reload();
            }
        },
    });
}

onDomReady(wireAudioUpload);
