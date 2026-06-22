// audioEngine plays the game sound effects and the per-question quiz clips
// through Howler.js, the way #1088 needs for reliable iOS playback.
//
// The iOS bug it fixes: with a plain <audio> element (and even a naive Web Audio
// attempt) the FIRST programmatic play after the Start tap is dropped on iOS,
// because it fires after an async decode -- outside the gesture -- before the
// output path is "hot"; every later clip then plays. The strategy:
//
//   1. Preload + DECODE the small SFX on page load, before any gesture
//      (decodeAudioData works while the AudioContext is suspended).
//   2. On the Start gesture, SYNCHRONOUSLY (before any await): resume the
//      context, start the iOS media-channel keep-alive, then immediately play
//      the round-start SFX. That genuine gesture-bound first play unlocks iOS
//      output.
//   3. The frequent SFX (round / question / answers / correct / wrong) keep the
//      context running between clips so iOS cannot re-suspend it. They are
//      load-bearing, not decoration.
//   4. Preload ALL question clips at game start so each question just plays an
//      already-decoded Howl with no per-question decode race.
//
// This is a plain (non-Alpine) controller. `view` is the owning Alpine
// component; the engine never holds Alpine-reactive state itself (per the
// frontend rules) -- it reads/writes flags ON `view` (view.audioMuted,
// view.audioLoading, view.audioBlocked) so the writes go through Alpine's proxy
// and re-render the surface.
import { loadAudioMuted, saveAudioMuted } from '@shared/audioMute.js';
import { AUDIO_FORMATS } from '@shared/audioFormats.js';
import { createIOSKeepAlive } from '@shared/iosKeepAlive.js';

// SFX are the game sound effects by logical name. Both surfaces import these
// constants and pass them to playEffect, so the names live in exactly one place
// (a rename or a typo can't drift between the engine and the two callers).
// answer-correct / answer-wrong are the solo per-pick stings; answer-reveal is
// the host big screen's neutral "here is the answer" sting (the big screen has
// no per-player pick).
export const SFX = {
    roundStart: 'round-start',
    questionShow: 'question-show',
    answersShow: 'answers-show',
    answerCorrect: 'answer-correct',
    answerWrong: 'answer-wrong',
    answerReveal: 'answer-reveal',
};

// EFFECT_SRC maps each SFX name to its served URL. These are small placeholder
// tones; the maintainer swaps real sounds in at the same paths.
const EFFECT_SRC = {
    [SFX.roundStart]: '/static/audio/sfx/round-start.mp3',
    [SFX.questionShow]: '/static/audio/sfx/question-show.mp3',
    [SFX.answersShow]: '/static/audio/sfx/answers-show.mp3',
    [SFX.answerCorrect]: '/static/audio/sfx/answer-correct.mp3',
    [SFX.answerWrong]: '/static/audio/sfx/answer-wrong.mp3',
    [SFX.answerReveal]: '/static/audio/sfx/answer-reveal.mp3',
};

// A repeat-flagged clip plays a fixed number of times with a fixed silent gap
// between plays (#1073), so the repeats read as distinct plays rather than one
// looped run.
const REPEAT_PLAYS = 3;
const REPEAT_GAP_MS = 1000;

// PRELOAD_CLIP_TIMEOUT_MS caps how long a single clip's load is waited on, and
// PRELOAD_BUDGET_MS caps the whole preload, so a slow or failed clip can never
// hang the game at start.
const PRELOAD_CLIP_TIMEOUT_MS = 8000;
const PRELOAD_BUDGET_MS = 12000;

// howlerGlobal returns the vendored Howl constructor, or null when the library
// is absent (the join phone deliberately omits it). Every entry point guards on
// this so the engine degrades to a silent no-op rather than throwing.
function howlerGlobal() {
    return typeof window !== 'undefined' ? window.Howl || null : null;
}

// howlerManager returns the global Howler manager (window.Howler), or null when
// absent. The manager owns the shared Web Audio context and the autoSuspend
// flag; callers below read both through this one lookup.
function howlerManager() {
    return typeof window !== 'undefined' ? window.Howler || null : null;
}

// keepContextAlive disables Howler's auto-suspend so the shared AudioContext
// stays running for the whole game. Howler suspends it after ~30s of silence by
// default, and resuming it for the next clip glitches/crackles the first moment
// of audio on many systems (it reads like a buffer underrun / a cold sound
// device). Live play has gaps longer than that between questions (#1088).
function keepContextAlive() {
    const manager = howlerManager();
    if (manager) manager.autoSuspend = false;
}

// audioContextRunning reports whether Howler's Web Audio context is actually
// producing sound. A play() that did not throw can still be silent if the
// context is "suspended" (e.g. a mid-game resume with no Start gesture to unlock
// it), so callers surface the manual play control when this is false. An absent
// context is treated as running (the join phone has no engine; nothing to flag).
function audioContextRunning() {
    const manager = howlerManager();
    const ctx = manager ? manager.ctx : null;
    return !ctx || ctx.state === 'running';
}

export function createAudioEngine(view) {
    // Preloaded SFX Howls, keyed by logical name. Built once by preloadEffects
    // and kept for the whole page lifetime (teardown does NOT unload them, so a
    // second game in the same SPA session still has its sounds).
    const effects = {};
    // Preloaded question clips, keyed by questionId, each
    // { howl, loaded, failed, repeat }.
    const clips = new Map();
    const keepAlive = createIOSKeepAlive();

    // The question whose clip has already been auto-played, so the once-per-
    // question guard makes a repeated init / tick a no-op rather than a restart.
    let lastPlayedQuestionId = null;
    // The currently-playing clip id, so stopClip can stop the right Howl.
    let activeClipId = null;
    // A monotonic token bumped on every stop / new clip, so a torn-down repeat
    // sequence's late 'end' callback cannot re-fire onto the next clip.
    let sequenceToken = 0;
    // Pending repeat-gap timer, cleared on stop.
    let repeatTimer = null;
    let unlocked = false;
    // True once preloadClips has resolved, so playClip can tell "the clip is
    // still on its way" (wait) from "there is genuinely no clip for this audio
    // question" (surface the manual fallback).
    let clipsReady = false;
    // The question whose clip the surface currently wants playing. Set by
    // playClip, cleared by stopClip; lets a play requested before its clip
    // finished preloading still fire the moment the clip is ready.
    let wantedClipQuestionId = null;

    function muted() {
        return !!view.audioMuted;
    }

    // preloadEffects builds one Howl per SFX and kicks off its load + decode.
    // Called on component init (page load), BEFORE any gesture: decodeAudioData
    // runs while the context is suspended, so the buffers are ready by the Start
    // tap. html5:false keeps them on the Web Audio path (the gesture unlocks).
    function preloadEffects() {
        const Howl = howlerGlobal();
        if (!Howl) return;
        keepContextAlive();
        for (const [name, src] of Object.entries(EFFECT_SRC)) {
            if (effects[name]) continue;
            effects[name] = new Howl({ src: [src], preload: true, html5: false, mute: muted() });
        }
    }

    // unlock runs SYNCHRONOUSLY first in the Start gesture: resume the
    // AudioContext, start the iOS keep-alive, mark unlocked. Best-effort and
    // never throws so a quirky environment cannot break the Start handler.
    // Idempotent, so the manual play control (replayClip) can call it again to
    // unlock output on a mid-game resume.
    function unlock() {
        try {
            // Belt-and-suspenders with preloadEffects: keep the context from
            // auto-suspending (and glitching the next clip) during the game.
            keepContextAlive();
            const manager = howlerManager();
            const ctx = manager ? manager.ctx : null;
            if (ctx && typeof ctx.resume === 'function') {
                // resume() returns a promise; we do not await it (the gesture
                // play below is what actually unlocks iOS output).
                const resumed = ctx.resume();
                if (resumed && typeof resumed.catch === 'function') resumed.catch(() => {});
            }
            keepAlive.start();
            unlocked = true;
        } catch {
            // Best-effort: a missing/quirky Howler must not break Start.
        }
    }

    // playEffect plays a preloaded SFX. Fire-and-forget; respects mute; no
    // fallback UI for SFX (they are not load-bearing for content, only flow).
    function playEffect(name) {
        if (muted()) return;
        const howl = effects[name];
        if (!howl) return;
        try {
            howl.play();
        } catch {
            // A play() that throws (rare) is swallowed: an SFX is non-critical.
        }
    }

    // preloadClips builds one Howl per manifest clip and resolves once every
    // clip has loaded, errored, or hit its per-clip timeout -- with an overall
    // budget so a slow/failed clip can never hang the game. Tracks per-clip
    // readiness so playClip knows whether to play immediately, wait, or fall
    // back. Returns a promise the caller may await behind a brief loading state.
    function preloadClips(manifest) {
        const Howl = howlerGlobal();
        // Accept either a raw clips array or the manifest object the endpoints
        // return ({ clips: [...] }), so each surface hands us the fetched payload
        // (or null on a failed fetch) directly without re-parsing the shape.
        const list = Array.isArray(manifest)
            ? manifest
            : (manifest && Array.isArray(manifest.clips) ? manifest.clips : []);
        if (!Howl || list.length === 0) {
            clipsReady = true;
            tryPlayWanted();
            return Promise.resolve();
        }

        const perClip = list.map((clip) => new Promise((resolve) => {
            if (clip == null || clip.questionId == null || !clip.audioUrl) {
                resolve();
                return;
            }
            const entry = { howl: null, loaded: false, failed: false, repeat: !!clip.audioRepeat };
            clips.set(clip.questionId, entry);
            let settled = false;
            const settle = () => {
                if (settled) return;
                settled = true;
                clearTimeout(timer);
                resolve();
            };
            const howl = new Howl({
                src: [clip.audioUrl],
                format: AUDIO_FORMATS,
                preload: true,
                html5: false,
                mute: muted(),
                onload: () => {
                    entry.loaded = true;
                    // Clear a stale failed flag: the per-clip timeout may have
                    // marked this failed before the load actually completed within
                    // the overall budget. A loaded clip is playable, so it must not
                    // stay flagged failed (every consumer checks failed first).
                    entry.failed = false;
                    settle();
                    // A clip the surface is already waiting on plays the moment it
                    // arrives (the host preloads in parallel with the SSE flow, so
                    // the first question can be wanted before its Howl loaded).
                    if (wantedClipQuestionId === clip.questionId) tryPlayWanted();
                },
                onloaderror: () => {
                    entry.failed = true;
                    settle();
                    if (wantedClipQuestionId === clip.questionId) tryPlayWanted();
                },
            });
            entry.howl = howl;
            const timer = setTimeout(() => {
                // A clip that neither loaded nor errored within the per-clip
                // budget is treated as failed, so tryPlayWanted surfaces the
                // manual fallback rather than silently dropping a stalled clip.
                if (!entry.loaded && !entry.failed) entry.failed = true;
                settle();
                if (wantedClipQuestionId === clip.questionId) tryPlayWanted();
            }, PRELOAD_CLIP_TIMEOUT_MS);
        }));

        // The Howls exist now (created synchronously above), so a play requested
        // before this call can be armed immediately rather than lost.
        tryPlayWanted();

        // Whichever finishes first: all clips settled, or the overall budget.
        // Clear the budget timer on settle so a fast all-loaded preload does not
        // leave a stray 12s timer ticking after every game start.
        let budgetTimer = null;
        const budget = new Promise((resolve) => { budgetTimer = setTimeout(resolve, PRELOAD_BUDGET_MS); });
        return Promise.race([Promise.all(perClip), budget]).then((result) => {
            if (budgetTimer !== null) clearTimeout(budgetTimer);
            clipsReady = true;
            // Now that preloading is done, a wanted clip that never materialized
            // (a manifest miss) can be flagged as the manual fallback.
            tryPlayWanted();
            return result;
        });
    }

    // armRepeat wires the repeat sequence for a clip: after each play ends, if
    // plays remain, wait the gap then re-arm and play again. token-guarded so a
    // torn-down sequence's late 'end' is ignored; the restart is stop()+play()
    // (not Howler seek(), which re-triggers a still-playing sound).
    function armRepeat(entry, token, playsRemaining) {
        const howl = entry.howl;
        if (!howl) return;
        const onEnd = () => {
            if (token !== sequenceToken) return;
            if (playsRemaining <= 1) return;
            const next = playsRemaining - 1;
            repeatTimer = setTimeout(() => {
                repeatTimer = null;
                if (token !== sequenceToken) return;
                try {
                    howl.stop();
                    howl.play();
                } catch {
                    // ignore: a failed repeat play is non-fatal.
                }
                armRepeat(entry, token, next);
            }, REPEAT_GAP_MS);
        };
        howl.once('end', onEnd);
    }

    // beginPlay stops any prior sequence, plays the clip from the top, and arms
    // the repeat sequence when the clip is repeat-flagged.
    function beginPlay(questionId, entry) {
        clearRepeatTimer();
        sequenceToken += 1;
        const token = sequenceToken;
        activeClipId = questionId;
        // Consume the once-per-question guard here, where the clip actually
        // plays, so a still-loading wait never swallows the play and a repeated
        // tick after play is a no-op.
        lastPlayedQuestionId = questionId;
        const howl = entry.howl;
        if (!howl) { view.audioBlocked = true; return; }
        try {
            howl.mute(muted());
            // Drop any stale 'end' listener left on this reused Howl by a prior
            // repeat sequence that was torn down before its 'end' fired, so the
            // listener set does not grow across replays (#1088).
            howl.off('end');
            howl.stop();
            howl.play();
        } catch {
            view.audioBlocked = true;
            return;
        }
        // Surface the manual play control only when there was no Start-gesture
        // unlock (a mid-game resume): there the context is suspended, so play()
        // does not throw but no sound comes out, and a tap (replayClip) is needed
        // to resume output. On the unlocked Start path we trust the gesture +
        // priming sound and do NOT flag blocked off a context that may still be
        // mid-resume() at this instant -- a one-time snapshot there would latch a
        // false "Play audio" for the whole question even though the clip plays.
        view.audioBlocked = !unlocked && !audioContextRunning();
        if (entry.repeat) armRepeat(entry, token, REPEAT_PLAYS);
    }

    // playClip asks the engine to play questionId's clip once (the guard makes a
    // re-render or a repeated SSE tick a no-op). It records the wanted question
    // and delegates to tryPlayWanted, so a play requested before its clip has
    // preloaded still fires the moment the clip is ready (the host preloads in
    // parallel with the SSE flow, so the first question can arrive before its
    // Howl exists).
    function playClip(questionId) {
        if (questionId == null || questionId === lastPlayedQuestionId) return;
        wantedClipQuestionId = questionId;
        tryPlayWanted();
    }

    // tryPlayWanted plays the wanted clip if it is ready, arms a one-shot play if
    // it is still loading, and surfaces the manual fallback if it failed or (once
    // preloading has finished) is genuinely absent. Re-run by preloadClips as
    // clips arrive and when the preload resolves, so a wanted-before-ready clip
    // is not lost. lastPlayedQuestionId is consumed only once a real decision is
    // made, so a wait does not permanently swallow the play, and re-entry after a
    // decision is a no-op (no double play / double-armed load handler).
    function tryPlayWanted() {
        const questionId = wantedClipQuestionId;
        if (questionId == null || questionId === lastPlayedQuestionId) return;
        const entry = clips.get(questionId);
        if (!entry || !entry.howl) {
            // No Howl yet. While preloading is in flight, wait -- preloadClips
            // re-runs this from each clip's onload as it arrives. Once preloading
            // is done and there is still no clip for this audio question, surface
            // the manual fallback -- but do NOT consume the once-guard, so a
            // later retry that loads the real clip can still autoplay it (only
            // beginPlay latches lastPlayedQuestionId).
            if (clipsReady) view.audioBlocked = true;
            return;
        }
        if (entry.failed) {
            // Failed (load error or stalled past the per-clip timeout): surface
            // the fallback, but again do not latch the guard, so a clip that
            // recovers (onload clears failed) can still autoplay.
            view.audioBlocked = true;
            return;
        }
        if (entry.loaded) {
            beginPlay(questionId, entry);
            return;
        }
        // The Howl exists but is still loading: do nothing now. preloadClips
        // attached the onload/onloaderror that re-run tryPlayWanted (or flag the
        // failure) when this clip settles -- a single load path, no duplicate
        // handlers. lastPlayedQuestionId is consumed only once the clip actually
        // plays (beginPlay) or is definitively unplayable, so a wait never
        // swallows the play.
    }

    // replayClip is the user-gesture path (the play / replay control): it clears
    // the fallback and bypasses the once-guard so the host/player can restart the
    // clip on demand. It also unlocks: on a mid-game resume (no Start tap) this
    // first tap is what resumes the context and starts the keep-alive, so the
    // clip is finally audible.
    function replayClip(questionId) {
        if (questionId == null) return;
        unlock();
        const entry = clips.get(questionId);
        if (!entry || !entry.howl) { view.audioBlocked = true; return; }
        if (entry.failed) { view.audioBlocked = true; return; }
        view.audioBlocked = false;
        if (entry.loaded) {
            beginPlay(questionId, entry);
            return;
        }
        // Still loading: re-arm the wanted-clip path rather than committing the
        // once-guard now. beginPlay would latch lastPlayedQuestionId, so a load
        // that then errors would be swallowed by tryPlayWanted's guard and never
        // re-surface the fallback. Resetting the guard lets onload play it or
        // onloaderror flag it.
        wantedClipQuestionId = questionId;
        lastPlayedQuestionId = null;
        tryPlayWanted();
    }

    function clearRepeatTimer() {
        if (repeatTimer !== null) {
            clearTimeout(repeatTimer);
            repeatTimer = null;
        }
    }

    // stopClip stops the current clip and any pending repeat, and drops the
    // wanted-clip request, so a still-playing clip cannot bleed into the next
    // question and a clip still preloading is not played after the advance.
    // Called on question advance.
    function stopClip() {
        clearRepeatTimer();
        sequenceToken += 1;
        wantedClipQuestionId = null;
        if (activeClipId != null) {
            const entry = clips.get(activeClipId);
            if (entry && entry.howl) {
                // off('end') drops the pending repeat listener so a clip stopped
                // mid-sequence does not leave a handler on the reused Howl.
                try { entry.howl.off('end'); entry.howl.stop(); } catch { /* ignore */ }
            }
            activeClipId = null;
        }
    }

    // cancelPendingClip drops a not-yet-started wanted clip WITHOUT stopping a
    // clip that is already playing. A surface calls it when the question phase
    // ends (e.g. the live reveal) so a clip that finishes loading late does not
    // autoplay over the reveal; a clip that already started during the question
    // keeps playing through, as before.
    function cancelPendingClip() {
        wantedClipQuestionId = null;
    }

    // toggleMute flips and persists the mute preference and applies it to ALL
    // live Howls (SFX + clips) at once, so a mid-clip toggle takes effect
    // immediately.
    function toggleMute() {
        const next = !view.audioMuted;
        view.audioMuted = next;
        saveAudioMuted(next);
        applyMute(next);
    }

    function applyMute(value) {
        for (const howl of Object.values(effects)) {
            try { howl.mute(value); } catch { /* ignore */ }
        }
        for (const entry of clips.values()) {
            if (entry.howl) {
                try { entry.howl.mute(value); } catch { /* ignore */ }
            }
        }
    }

    // teardown stops the keep-alive and unloads the per-game question clips
    // (game end / component destroy) so no audio or timer leaks across games.
    // The SFX Howls are deliberately left loaded for the page lifetime, so a
    // second game in the same SPA session still has its sounds (preloadEffects
    // skips already-built effects and is not re-run on a same-session restart).
    function teardown() {
        clearRepeatTimer();
        sequenceToken += 1;
        wantedClipQuestionId = null;
        clipsReady = false;
        keepAlive.stop();
        for (const entry of clips.values()) {
            if (entry.howl) {
                try { entry.howl.unload(); } catch { /* ignore */ }
            }
        }
        clips.clear();
        activeClipId = null;
        lastPlayedQuestionId = null;
    }

    return {
        preloadEffects,
        unlock,
        playEffect,
        preloadClips,
        playClip,
        replayClip,
        stopClip,
        cancelPendingClip,
        toggleMute,
        muted,
        teardown,
        // isUnlocked is exposed for tests / diagnostics; the engine itself never
        // gates on it (unlock is best-effort).
        isUnlocked: () => unlocked,
    };
}

// initialMuted seeds a surface's mute flag from the persisted preference, so
// both audio surfaces import their initial audio state from one module.
export function initialMuted() {
    return loadAudioMuted();
}
