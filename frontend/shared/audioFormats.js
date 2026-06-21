// audioFormats lists the audio container formats the media upload accepts
// (mirrors the server's audio sniffing in internal/media/audio.go: mp3 / m4a /
// ogg / wav). The question audio URL is /media/{id} with no file extension, so
// Howler.js cannot infer the codec from the URL; passing this list as the Howl's
// `format` gets it past its extension check. Under Web Audio (the default) the
// browser's decodeAudioData then decodes the actual bytes, so the hint only has
// to name a codec the browser claims to support - the real format is read off
// the data (#1088).
export const AUDIO_FORMATS = ['mp3', 'm4a', 'ogg', 'wav'];
