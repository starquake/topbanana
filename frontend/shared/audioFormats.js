// AUDIO_FORMATS is the Howler codec hint for the question clips, which are
// served from the extension-less /media/{id} route (the URL carries no file
// extension Howler could sniff). Listing the formats the upload pipeline accepts
// lets Howler pick the right decoder without a Content-Type round trip (#1088).
export const AUDIO_FORMATS = ['mp3', 'm4a', 'ogg', 'wav'];
