// questionAudio holds the play / mute / replay / per-question-guard logic for
// the per-question audio (#1059), shared by the solo client (GameApp) and the
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

// A repeating question plays its clip a fixed number of times with a fixed
// silent gap between plays (#1073). A second of silence separates the repeats so
// they read as distinct plays rather than one looped run.
const AUDIO_REPEAT_PLAYS = 3;
const AUDIO_REPEAT_GAP_MS = 1000;

// createQuestionAudio builds a controller that owns the per-question guard
// state. Its methods take the live host on each call so all reactive writes go
// through Alpine's proxy.
export function createQuestionAudio() {
    // The id of the question whose clip has already been started, so a repeat
    // call for the same question is a no-op rather than a restart.
    let lastPlayedQuestionId = null;

    // Repeat-sequence state for the clip currently in flight. The pending timer
    // and the ended listener are torn down before each new start and on stop, so
    // a queued replay can never fire onto the next question's clip.
    let playsRemaining = 1;
    let repeatTimer = null;
    let endedHandler = null;

    // clearRepeat tears down the in-flight repeat sequence: it removes the ended
    // listener from the element and cancels any pending gap timer, so neither can
    // fire after the question moves on or a fresh start begins.
    function clearRepeat(el) {
        if (repeatTimer !== null) {
            clearTimeout(repeatTimer);
            repeatTimer = null;
        }
        if (endedHandler && el && typeof el.removeEventListener === 'function') {
            el.removeEventListener('ended', endedHandler);
        }
        endedHandler = null;
    }

    // playFromTop resets the element to the start, re-applies the mute
    // preference, and plays it, surfacing the manual control when autoplay is
    // blocked. The repeat replays reuse this so each play behaves like the first.
    function playFromTop(host, el) {
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

    // start plays the clip from the top for the given question id, honouring the
    // per-question guard: the first call for an id plays it, later calls for the
    // same id are ignored. force=true bypasses the guard for an explicit user
    // gesture (replay / manual play). audioRepeat enables the repeat sequence for
    // this play.
    function start(host, questionId, force = false, audioRepeat = false) {
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
        clearRepeat(el);
        playsRemaining = audioRepeat ? AUDIO_REPEAT_PLAYS : 1;
        if (playsRemaining > 1) {
            endedHandler = () => {
                if (playsRemaining <= 1) return;
                playsRemaining -= 1;
                repeatTimer = setTimeout(() => {
                    repeatTimer = null;
                    playFromTop(host, el);
                }, AUDIO_REPEAT_GAP_MS);
            };
            el.addEventListener('ended', endedHandler);
        }
        playFromTop(host, el);
    }

    // replay restarts the clip from the play / replay control. The click is a
    // user gesture, so it clears the blocked fallback and bypasses the guard. The
    // host passes the current question's repeat flag so a manual replay honours
    // it too.
    function replay(host, audioRepeat = false) {
        host.audioBlocked = false;
        start(host, null, true, audioRepeat);
    }

    // stop pauses the current clip so a still-playing audio clip does not bleed into
    // the next question or the end-of-game screen (#1070). It also tears down any
    // pending repeat so a queued replay cannot fire onto the next question's clip.
    // No-ops when no element is mounted.
    function stop(host) {
        const el = host.getAudioEl();
        clearRepeat(el);
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
