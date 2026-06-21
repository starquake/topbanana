// questionAudio holds the play / mute / replay / per-question-guard logic for
// the per-question audio (#1059), shared by the solo client (GameApp) and the
// host big screen so the two surfaces stay in lockstep (#1070). It drives a
// Howler `Howl` (#1088) instead of an HTMLAudioElement: Howler uses Web Audio
// under the hood and auto-unlocks the AudioContext on the first user
// interaction (the Start tap), then plays freely with no further gesture. The
// old `<audio>` element needed a fresh user gesture per clip on iOS, so later
// questions went silent unless the player kept tapping; routing playback
// through Howler fixes that.
//
// Trade-off (#1088): Howler defaults to Web Audio, which iOS silences when the
// ringer/mute switch is on (the old `<audio>` played through it). This first cut
// accepts that and respects the hardware mute switch; it does NOT add the
// silent-audio keep-alive workaround. If that is reported, options (b)/(c)
// (a silent keep-alive track, or html5:true) exist - but html5:true weakens the
// iOS autoplay fix this change makes, so it is deliberately not used here.
//
// The host MUST be the live Alpine `this` at call time, not one captured at
// construction: Alpine tracks reactivity through a Proxy, so a write to
// host.audioMuted / host.audioBlocked only re-renders when it goes through the
// proxy the component method was invoked with. Capturing accessors in the
// constructor would write to the raw object and silently skip the re-render.
//
// The host shape is:
//   host.audioMuted     -> current mute preference (read + written here)
//   host.audioBlocked   -> autoplay-blocked fallback flag (written here)
//
// The per-question guard mirrors the big screen's once-per-question-id rule:
// start() only begins a clip the first time it is called for a given question
// id, so a re-render mid-question (solo) or a repeated SSE tick (big screen)
// cannot restart the clip.
import { loadAudioMuted, saveAudioMuted } from '@shared/audioMute.js';
import { AUDIO_FORMATS } from '@shared/audioFormats.js';

// A repeating question plays its clip a fixed number of times with a fixed
// silent gap between plays (#1073). A second of silence separates the repeats so
// they read as distinct plays rather than one looped run.
const AUDIO_REPEAT_PLAYS = 3;
const AUDIO_REPEAT_GAP_MS = 1000;

// howlConstructor returns the global Howl constructor, or null when the vendor
// script has not loaded (or in a non-browser context). A missing global means
// audio is genuinely unavailable, surfaced through the blocked fallback.
function howlConstructor() {
    return typeof window !== 'undefined' ? window.Howl : null;
}

// createQuestionAudio builds a controller that owns the per-question guard
// state and the live Howl. Its methods take the live host on each call so all
// reactive writes go through Alpine's proxy.
export function createQuestionAudio() {
    // The id of the question whose clip has already been started, so a repeat
    // call for the same question is a no-op rather than a restart.
    let lastPlayedQuestionId = null;

    // The live Howl for the current clip and the URL it was built for. A fresh
    // start for a different URL unloads the prior Howl and builds a new one;
    // reusing the same URL keeps the existing Howl so a replay does not refetch.
    let howl = null;
    let howlUrl = null;

    // Repeat-sequence state for the clip currently in flight. The pending timer
    // is torn down before each new start and on stop, so a queued replay can
    // never fire onto the next question's clip. repeatToken identifies the
    // current sequence: clearRepeat bumps it, so a stray 'end' or a fired timer
    // left over from a torn-down sequence (e.g. a real Howler 'end' from the
    // initial autoplay arriving after a replay re-armed the loop) sees a stale
    // token and is ignored. This makes the repeat exactly-N robust against
    // duplicate or late 'end' events rather than relying on one persistent
    // listener firing exactly once per play.
    let repeatTimer = null;
    let repeatToken = 0;

    // clearRepeat tears down the in-flight repeat sequence: it cancels any
    // pending gap timer, removes the Howl's 'end' listeners, and bumps the
    // sequence token so a stale once-handler or a fired timer that was already
    // queued cannot schedule another play after the question moves on or a fresh
    // start begins.
    function clearRepeat() {
        if (repeatTimer !== null) {
            clearTimeout(repeatTimer);
            repeatTimer = null;
        }
        if (howl) howl.off('end');
        repeatToken += 1;
    }

    // disposeHowl tears down the repeat sequence and unloads the current Howl so
    // its Web Audio resources are freed. Called before building a Howl for a new
    // URL and on stop when the surface is moving away from the clip.
    function disposeHowl() {
        clearRepeat();
        if (howl) {
            howl.stop();
            howl.unload();
        }
        howl = null;
        howlUrl = null;
    }

    // ensureHowl returns a Howl for the given URL, building (and caching) one
    // when the URL changed. Returns null when the Howl global is unavailable.
    function ensureHowl(host, url) {
        const Howl = howlConstructor();
        if (!Howl || !url) return null;
        if (howl && howlUrl === url) return howl;
        disposeHowl();
        howl = new Howl({
            src: [url],
            // The audio URL is /media/{id} with no file extension, so Howler
            // cannot infer the codec; declare the accepted formats so it gets
            // past its extension check (Web Audio then decodes the real bytes).
            format: AUDIO_FORMATS,
            // Web Audio (the default) is what gives the iOS autoplay fix; do not
            // set html5:true here (#1088).
            mute: host.audioMuted,
            // A playerror surfaces the manual play control rather than failing
            // silently; the click then replays through the user-gesture path.
            onplayerror: () => { host.audioBlocked = true; },
        });
        howlUrl = url;
        return howl;
    }

    // playFromTop rewinds the Howl, re-applies the mute preference, and plays it,
    // clearing the blocked fallback once playback begins. The repeat replays
    // reuse this so each play behaves like the first. It stops the Howl before
    // playing rather than seeking to 0: stop() rewinds to the start AND cancels
    // any in-flight playback (and the real Howler 'end' that playback would emit),
    // so a previous play cannot leak a stray 'end' into the next. Howler's
    // seek(value) restarts a still-playing sound by calling play() again under the
    // hood, which would double-fire the repeat sequence; stop() + play() avoids
    // that and starts cleanly from the top.
    function playFromTop(host) {
        if (!howl) return;
        howl.mute(host.audioMuted);
        howl.stop();
        howl.play();
        host.audioBlocked = false;
    }

    // playWithRepeats plays the clip from the top exactly `plays` times with a
    // fixed silent gap between plays. clearRepeat starts a fresh sequence (new
    // token, no pending timer, no stale 'end' listener). Each subsequent play is
    // scheduled from a one-shot howl.once('end') re-armed per play: `once`
    // auto-removes after the first 'end', so a duplicate 'end' (e.g. a spy and
    // the real Howler both emitting, or a late real 'end' from a prior play)
    // cannot double-schedule. The captured token guards against an 'end' that
    // belongs to a torn-down sequence. Split from start() so the initial play
    // and the manual replay share one code path.
    function playWithRepeats(host, audioRepeat) {
        clearRepeat();
        const total = audioRepeat ? AUDIO_REPEAT_PLAYS : 1;
        const token = repeatToken;
        let remaining = total;

        function armNext() {
            if (remaining <= 0) return;
            howl.once('end', () => {
                if (token !== repeatToken || remaining <= 0) return;
                repeatTimer = setTimeout(() => {
                    repeatTimer = null;
                    if (token !== repeatToken || remaining <= 0) return;
                    playOnce();
                }, AUDIO_REPEAT_GAP_MS);
            });
        }

        function playOnce() {
            remaining -= 1;
            armNext();
            playFromTop(host);
        }

        playOnce();
    }

    // start plays the clip from the top for the given question id, honouring the
    // per-question guard: the first call for an id plays it, later calls for the
    // same id are ignored. force=true bypasses the guard for an explicit user
    // gesture (replay / manual play). audioRepeat enables the repeat sequence for
    // this play. url is the question's audioUrl; a missing Howl global / url
    // means audio is unavailable, surfaced as the blocked fallback.
    function start(host, questionId, url, force = false, audioRepeat = false) {
        if (!force) {
            if (questionId == null || questionId === lastPlayedQuestionId) return;
            lastPlayedQuestionId = questionId;
        }
        const sound = ensureHowl(host, url);
        if (!sound) { host.audioBlocked = true; return; }
        playWithRepeats(host, audioRepeat);
    }

    // replay restarts the clip from the play / replay control. The click is a
    // user gesture, so it clears the blocked fallback and bypasses the guard. The
    // host passes the current question's url + repeat flag so a manual replay
    // honours both.
    function replay(host, url, audioRepeat = false) {
        host.audioBlocked = false;
        start(host, null, url, true, audioRepeat);
    }

    // stop pauses the current clip so a still-playing audio clip does not bleed
    // into the next question or the end-of-game screen (#1070). It unloads the
    // Howl so its Web Audio resources are freed and the next question builds a
    // fresh one. It also tears down any pending repeat so a queued replay cannot
    // fire onto the next question's clip. No-ops when no Howl is live.
    function stop() {
        disposeHowl();
    }

    // toggleMute flips and persists the mute preference, applying it to the live
    // Howl at once so a mid-clip toggle takes effect immediately.
    function toggleMute(host) {
        const next = !host.audioMuted;
        host.audioMuted = next;
        saveAudioMuted(next);
        if (howl) howl.mute(next);
    }

    return { start, replay, stop, toggleMute };
}

// initialMuted seeds a surface's mute flag from the persisted preference, kept
// here so both surfaces import their audio state from one module.
export function initialMuted() {
    return loadAudioMuted();
}
