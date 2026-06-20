// admin.js is the combined admin/auth/home bundle (#1071). It imports the
// small standalone page modules so their top-level init runs from one loaded
// file instead of seven separate dist bundles. Each imported module guards its
// own setup (it queries for its anchor element, runs through onDomReady, or
// registers an alpine:init handler), so loading the combined bundle on a page
// where a feature's anchor is absent is a no-op for that feature.

import './cooldown.js';
import './copy-prompt.js';
import './password-length.js';
import './quiz-reorder.js';
import './quiz-image-upload.js';
import './quiz-audio-upload.js';
import './home.js';
