// audioMute persists the player's mute preference for question sounds (#1059)
// in localStorage so a player who mutes once stays muted across questions,
// reloads, and quizzes. Defaults to unmuted. Both the solo client and the host
// big screen read/write the same key so a host's choice on the big screen also
// sticks.
const AUDIO_MUTE_KEY = 'tb.audioMuted';

// loadAudioMuted returns the saved mute preference, or false (unmuted) when no
// preference is stored or localStorage is unavailable (private mode, SSR).
export function loadAudioMuted() {
    try {
        return window.localStorage.getItem(AUDIO_MUTE_KEY) === '1';
    } catch {
        return false;
    }
}

// saveAudioMuted persists the mute preference. Best-effort: a storage failure
// (quota, private mode) is swallowed so a mute toggle never throws.
export function saveAudioMuted(muted) {
    try {
        window.localStorage.setItem(AUDIO_MUTE_KEY, muted ? '1' : '0');
    } catch {
        // ignore: persistence is best-effort.
    }
}
