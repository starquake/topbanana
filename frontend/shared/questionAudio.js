// questionAudio holds the play / mute / replay / per-question-guard logic for
// the per-question sound (#1059), shared by the solo client (GameApp) and the
// host big screen so the two surfaces stay in lockstep (#1070). Both surfaces
// own a single <audio> element and a `muted` / `blocked` UI flag; this module
// drives that element and those flags through a small host object passed on
// each call, so it stays free of any Alpine / DOM coupling of its own.
//
// The host MUST be the live Alpine `this` at call time, not one captured at
// construction: Alpine tracks reactivity through a Proxy, so a write to
// host.audioMuted / host.audioBlocked only re-renders when it goes through the
// proxy the component method was invoked with. Capturing accessors in the
// constructor would write to the raw object and silently skip the re-render.
//
// The host shape is:
//   host.getAudioEl()   -> the live <audio> element, or null when not mounted
//   host.audioMuted     -> current mute preference (read + written here)
//   host.audioBlocked   -> autoplay-blocked fallback flag (written here)
//
// The per-question guard mirrors the big screen's once-per-question-id rule:
// start() only begins a clip the first time it is called for a given question
// id, so a re-render mid-question (solo) or a repeated SSE tick (big screen)
// cannot restart the clip.
import { loadAudioMuted, saveAudioMuted } from '@shared/audioMute.js';

// createQuestionAudio builds a controller that owns the per-question guard
// state. Its methods take the live host on each call so all reactive writes go
// through Alpine's proxy.
export function createQuestionAudio() {
    // The id of the question whose clip has already been started, so a repeat
    // call for the same question is a no-op rather than a restart.
    let lastPlayedQuestionId = null;

    // start plays the clip from the top for the given question id, honouring the
    // per-question guard: the first call for an id plays it, later calls for the
    // same id are ignored. force=true bypasses the guard for an explicit user
    // gesture (replay / manual play).
    function start(host, questionId, force = false) {
        if (!force) {
            if (questionId == null || questionId === lastPlayedQuestionId) return;
            lastPlayedQuestionId = questionId;
        }
        const el = host.getAudioEl();
        if (!el) {
            // Element not mounted yet (an x-if / $nextTick race): surface the
            // play control so the player can start the clip manually.
            host.audioBlocked = true;
            return;
        }
        el.muted = host.audioMuted;
        try {
            el.currentTime = 0;
        } catch {
            // Some browsers reject currentTime before metadata loads; harmless.
        }
        const playback = el.play();
        if (playback && typeof playback.then === 'function') {
            playback.then(() => {
                host.audioBlocked = false;
            }).catch(() => {
                host.audioBlocked = true;
            });
        }
    }

    // replay restarts the clip from the play / replay control. The click is a
    // user gesture, so it clears the blocked fallback and bypasses the guard.
    function replay(host) {
        host.audioBlocked = false;
        start(host, null, true);
    }

    // stop pauses the current clip so a still-playing sound does not bleed into
    // the next question or the end-of-game screen (#1070). No-ops when no
    // element is mounted.
    function stop(host) {
        const el = host.getAudioEl();
        if (el && typeof el.pause === 'function') el.pause();
    }

    // toggleMute flips and persists the mute preference, applying it to the live
    // element at once so a mid-clip toggle takes effect immediately.
    function toggleMute(host) {
        const next = !host.audioMuted;
        host.audioMuted = next;
        saveAudioMuted(next);
        const el = host.getAudioEl();
        if (el) el.muted = next;
    }

    return { start, replay, stop, toggleMute };
}

// initialMuted seeds a surface's mute flag from the persisted preference, kept
// here so both surfaces import their audio state from one module.
export function initialMuted() {
    return loadAudioMuted();
}
