// audioEngine plays the game SFX and per-question quiz clips through Howler.js
// for reliable iOS autoplay (#1088). iOS drops the first programmatic play after
// the Start tap (it fires after an async decode, outside the gesture), so the
// strategy is: decode the SFX on page load, then on the Start gesture resume the
// context + start the keep-alive + play the round-start SFX (a gesture-bound play
// that unlocks output), and preload every clip up front so no per-question decode
// races the reveal.
//
// `view` is the owning Alpine component; the engine holds no reactive state of its
// own (per the frontend rules) and writes audioMuted/audioLoading/audioBlocked on
// `view` so the writes go through Alpine's proxy.
import { loadAudioMuted, saveAudioMuted } from '@shared/audioMute.js';
import { AUDIO_FORMATS } from '@shared/audioFormats.js';
import { createIOSKeepAlive } from '@shared/iosKeepAlive.js';

// SFX names live here once so the engine and both calling surfaces can't drift.
export const SFX = {
    roundStart: 'round-start',
    questionShow: 'question-show',
    answersShow: 'answers-show',
    answerCorrect: 'answer-correct',
    answerWrong: 'answer-wrong',
    answerReveal: 'answer-reveal',
};

// Placeholder tones; the maintainer swaps real sounds in at the same paths.
const EFFECT_SRC = {
    [SFX.roundStart]: '/static/audio/sfx/round-start.mp3',
    [SFX.questionShow]: '/static/audio/sfx/question-show.mp3',
    [SFX.answersShow]: '/static/audio/sfx/answers-show.mp3',
    [SFX.answerCorrect]: '/static/audio/sfx/answer-correct.mp3',
    [SFX.answerWrong]: '/static/audio/sfx/answer-wrong.mp3',
    [SFX.answerReveal]: '/static/audio/sfx/answer-reveal.mp3',
};

// A repeat-flagged clip plays REPEAT_PLAYS times with a gap, so the repeats read
// as distinct plays rather than one looped run (#1073).
const REPEAT_PLAYS = 3;
const REPEAT_GAP_MS = 1000;

// SFX play below full volume so they don't compete with question audio clips.
const SFX_VOLUME = 0.5;

// Per-clip and overall preload caps so a slow/failed clip can never hang start.
const PRELOAD_CLIP_TIMEOUT_MS = 8000;
const PRELOAD_BUDGET_MS = 12000;

// Howl constructor / Howler manager, or null when the library is absent (the join
// phone omits it); every entry point guards on these so the engine no-ops rather
// than throwing.
function howlerGlobal() {
    return typeof window !== 'undefined' ? window.Howl || null : null;
}

function howlerManager() {
    return typeof window !== 'undefined' ? window.Howler || null : null;
}

// Disable Howler's auto-suspend so the context stays running: resuming it after
// its ~30s idle suspend glitches the first moment of the next clip (#1088).
function keepContextAlive() {
    const manager = howlerManager();
    if (manager) manager.autoSuspend = false;
}

// True when the Web Audio context is actually producing sound. A suspended
// context (a resume with no Start gesture) plays silently without throwing, so
// callers surface the manual control when this is false. Absent context = no
// engine (join phone), treated as running.
function audioContextRunning() {
    const manager = howlerManager();
    const ctx = manager ? manager.ctx : null;
    return !ctx || ctx.state === 'running';
}

export function createAudioEngine(view) {
    // SFX Howls by name; kept for the page lifetime (teardown does not unload them
    // so a second same-session game still has its sounds).
    const effects = {};
    // Question clips by questionId: { howl, loaded, failed, repeat }.
    const clips = new Map();
    const keepAlive = createIOSKeepAlive();

    // Once-per-question guard: the question whose clip has auto-played.
    let lastPlayedQuestionId = null;
    let activeClipId = null;
    // Bumped on every stop / new clip so a torn-down repeat's late 'end' is ignored.
    let sequenceToken = 0;
    let repeatTimer = null;
    let unlocked = false;
    // True once preloadClips resolved, so a missing clip means fall back rather
    // than keep waiting.
    let clipsReady = false;
    // The clip the surface wants playing, so a play requested before its Howl
    // loaded still fires when it is ready.
    let wantedClipQuestionId = null;

    function muted() {
        return !!view.audioMuted;
    }

    // Decode the SFX on page load (before any gesture) so they are ready for the
    // gesture-bound round-start play; html5:false keeps them on the Web Audio path.
    function preloadEffects() {
        const Howl = howlerGlobal();
        if (!Howl) return;
        keepContextAlive();
        for (const [name, src] of Object.entries(EFFECT_SRC)) {
            if (effects[name]) continue;
            effects[name] = new Howl({ src: [src], preload: true, html5: false, mute: muted(), volume: SFX_VOLUME });
        }
    }

    // Call SYNCHRONOUSLY first in the Start gesture: resume the context + start the
    // keep-alive. Best-effort and idempotent, so replayClip can re-unlock output on
    // a mid-game resume.
    function unlock() {
        try {
            keepContextAlive();
            const manager = howlerManager();
            const ctx = manager ? manager.ctx : null;
            // Not awaited: the gesture-bound play is what unlocks iOS output.
            if (ctx && typeof ctx.resume === 'function') {
                const resumed = ctx.resume();
                if (resumed && typeof resumed.catch === 'function') resumed.catch(() => {});
            }
            keepAlive.start();
            unlocked = true;
        } catch {
            // A missing/quirky Howler must not break Start.
        }
    }

    // Fire-and-forget; respects mute; no fallback UI (SFX are flow, not content).
    function playEffect(name) {
        if (muted()) return;
        const howl = effects[name];
        if (!howl) return;
        try {
            howl.play();
        } catch {
            // An SFX is non-critical.
        }
    }

    // Play an SFX then fire callback once it ends, so a question clip can follow
    // the question-show sting without overlapping it. Token-guarded: a question
    // advance or teardown during the SFX bumps sequenceToken and the callback
    // no-ops. If muted or the Howl is missing, the callback fires immediately so
    // the clip still plays.
    function playEffectThen(name, callback) {
        if (muted()) { callback(); return; }
        const howl = effects[name];
        if (!howl) { callback(); return; }
        const token = sequenceToken;
        try {
            howl.once('end', () => {
                if (token === sequenceToken) callback();
            });
            howl.once('stop', () => {
                if (token === sequenceToken) callback();
            });
            howl.play();
        } catch {
            callback();
        }
    }

    // Build a Howl per clip and resolve once all have loaded/errored/timed out,
    // bounded by an overall budget. Accepts the raw manifest ({clips:[...]}) or an
    // array (or null on a failed fetch), so a surface hands over its fetched
    // payload without re-parsing.
    function preloadClips(manifest) {
        const Howl = howlerGlobal();
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
                    // Clear a stale timeout-set failed flag: a loaded clip is
                    // playable, and every consumer checks failed first.
                    entry.failed = false;
                    settle();
                    // Play it now if the surface is already waiting on it.
                    if (wantedClipQuestionId === clip.questionId) tryPlayWanted();
                },
                onloaderror: () => {
                    entry.failed = true;
                    settle();
                    if (wantedClipQuestionId === clip.questionId) tryPlayWanted();
                },
            });
            entry.howl = howl;
            // Treat a stall as failed so tryPlayWanted surfaces the fallback.
            const timer = setTimeout(() => {
                if (!entry.loaded && !entry.failed) entry.failed = true;
                settle();
                if (wantedClipQuestionId === clip.questionId) tryPlayWanted();
            }, PRELOAD_CLIP_TIMEOUT_MS);
        }));

        // The Howls exist now, so a play wanted before this call can arm.
        tryPlayWanted();

        let budgetTimer = null;
        const budget = new Promise((resolve) => { budgetTimer = setTimeout(resolve, PRELOAD_BUDGET_MS); });
        return Promise.race([Promise.all(perClip), budget]).then((result) => {
            if (budgetTimer !== null) clearTimeout(budgetTimer);
            clipsReady = true;
            // A wanted clip that never materialized can now fall back.
            tryPlayWanted();
            return result;
        });
    }

    // After each play ends, wait the gap then re-arm and play again until the
    // repeat count is spent. token-guarded so a torn-down sequence's late 'end' is
    // ignored; restart is stop()+play() (seek() re-triggers a playing sound).
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
                    // A failed repeat play is non-fatal.
                }
                armRepeat(entry, token, next);
            }, REPEAT_GAP_MS);
        };
        howl.once('end', onEnd);
    }

    // Play the clip from the top and arm its repeat sequence. Latches the
    // once-guard here, where the clip actually plays.
    function beginPlay(questionId, entry) {
        clearRepeatTimer();
        sequenceToken += 1;
        const token = sequenceToken;
        activeClipId = questionId;
        lastPlayedQuestionId = questionId;
        const howl = entry.howl;
        if (!howl) { view.audioBlocked = true; return; }
        try {
            howl.mute(muted());
            // Drop a stale 'end' from a prior torn-down repeat so handlers don't grow.
            howl.off('end');
            howl.stop();
            howl.play();
        } catch {
            view.audioBlocked = true;
            return;
        }
        // Flag blocked only on a non-unlocked path (a mid-game resume), where the
        // suspended context plays silently. On the unlocked Start path we trust the
        // gesture and don't latch a false "Play audio" off a context that may still
        // be mid-resume().
        view.audioBlocked = !unlocked && !audioContextRunning();
        if (entry.repeat) armRepeat(entry, token, REPEAT_PLAYS);
    }

    // Play questionId's clip once (a re-render or repeated tick is a no-op). Records
    // the wanted question and defers to tryPlayWanted, so a play requested before
    // the clip loaded still fires when it is ready.
    function playClip(questionId) {
        if (questionId == null || questionId === lastPlayedQuestionId) return;
        wantedClipQuestionId = questionId;
        tryPlayWanted();
    }

    // Play the wanted clip if ready, wait if still loading (preloadClips re-runs
    // this from onload/onloaderror/timeout), or fall back if failed/absent. Only
    // beginPlay latches the once-guard, so a wait never swallows the play and a
    // recovered clip can still autoplay.
    function tryPlayWanted() {
        const questionId = wantedClipQuestionId;
        if (questionId == null || questionId === lastPlayedQuestionId) return;
        const entry = clips.get(questionId);
        if (!entry || !entry.howl) {
            if (clipsReady) view.audioBlocked = true;
            return;
        }
        if (entry.failed) {
            view.audioBlocked = true;
            return;
        }
        if (entry.loaded) {
            beginPlay(questionId, entry);
        }
        // Still loading: preloadClips' handlers re-run this when the clip settles.
    }

    // User-gesture path (play/replay control): unlocks (so a mid-game resume's
    // first tap resumes output), clears the fallback, and bypasses the once-guard.
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
        // Still loading: re-arm the wanted path instead of latching the guard, so a
        // later load error still re-surfaces the fallback.
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

    // Stop the current clip + pending repeat and drop the wanted request, so a clip
    // can't bleed into the next question. Called on question advance.
    function stopClip() {
        clearRepeatTimer();
        sequenceToken += 1;
        wantedClipQuestionId = null;
        if (activeClipId != null) {
            const entry = clips.get(activeClipId);
            if (entry && entry.howl) {
                try { entry.howl.off('end'); entry.howl.stop(); } catch { /* ignore */ }
            }
            activeClipId = null;
        }
    }

    // Drop a not-yet-started wanted clip WITHOUT stopping a playing one, so a clip
    // that loads late (e.g. after the live reveal) doesn't autoplay over it.
    function cancelPendingClip() {
        wantedClipQuestionId = null;
    }

    // Flip + persist mute and apply it to all live Howls at once.
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

    // Stop the keep-alive and unload the per-game clips on game end. The SFX Howls
    // stay loaded for the page lifetime (reused by a second same-session game).
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
        playEffectThen,
        preloadClips,
        playClip,
        replayClip,
        stopClip,
        cancelPendingClip,
        toggleMute,
        muted,
        teardown,
        // For tests/diagnostics; the engine never gates on it.
        isUnlocked: () => unlocked,
    };
}

// Seed a surface's mute flag from the persisted preference.
export function initialMuted() {
    return loadAudioMuted();
}
