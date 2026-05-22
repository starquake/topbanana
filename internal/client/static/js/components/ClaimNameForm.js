// claimNameForm is the Alpine x-data factory for the shared
// "pick a name" form rendered in two contexts: the pre-leaderboard
// prompt and the start-screen "set your name" affordance. Each
// instance owns its own input value, submitting flag, and error
// text, so opening the start-screen form doesn't carry stray state
// from a dismissed pre-leaderboard form.
//
// The parent gameApp injects two callbacks via Alpine's $data:
//   onSubmit(username) -> { ok, message }  (resolves to the
//       PlayerService.claimName result, with errors already mapped to
//       human-friendly messages)
//   onCancel()                              (optional; controls how the
//       form closes — collapse inline, dismiss modal, skip to
//       leaderboard, etc.)
//
// Keeping rendering and state inside Alpine (rather than building it
// into a single shared <template>) avoids the awkwardness of sharing
// reactive input/error state across three separate form instances on
// the page at once.
export function claimNameForm({ initialValue = '', cancelLabel = 'Cancel', submitLabel = 'Save', onSubmit, onCancel } = {}) {
    return {
        username: initialValue,
        submitting: false,
        error: '',
        cancelLabel,
        submitLabel,
        async submit() {
            if (this.submitting) return;
            const trimmed = (this.username || '').trim();
            if (trimmed === '') {
                this.error = 'Please enter a name.';
                return;
            }
            this.submitting = true;
            this.error = '';
            try {
                const result = await onSubmit(trimmed);
                if (!result || !result.ok) {
                    this.error = (result && result.message) || "Couldn't save your name — try again later.";
                    return;
                }
                // On success the parent component swaps the surrounding
                // UI; we deliberately don't reset `username` here so a
                // reopened form would inherit the saved value if needed.
            } finally {
                this.submitting = false;
            }
        },
        cancel() {
            if (this.submitting) return;
            if (typeof onCancel === 'function') onCancel();
        },
    };
}
