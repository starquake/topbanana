# Media uploads

The media upload route (`POST /admin/quizzes/{quizID}/media`) has a per-request file count cap (`maxUploadFilesPerRequest`), but the form JS fires **one request per picked file**, so that cap alone is bypassable by a runaway/malicious client. Two server-side backstops in `internal/mediahttp/upload.go` close the gap (#988); any new upload-style route must compose the same shape rather than trusting the per-request cap:

- **Per-host file budget** (`UploadBudgetLimiter`): a sliding-window limiter keyed by player id that charges the **file count** per request, so N single-file POSTs draw down the same budget one N-file batch would. Over budget returns **429** with `Retry-After`. Config: `MEDIA_UPLOAD_BUDGET` (default 60) over `MEDIA_UPLOAD_BUDGET_WINDOW` (default 1m); zero disables.
- **Per-quiz library cap**: rejects an upload that would push a quiz over its image ceiling with **409**, checked **before** the budget charge so a clear admin denial does not also spend the host's rate budget. Config: `MEDIA_QUIZ_IMAGE_LIMIT` (default 200); zero disables.

The friendly-client half is a JS concurrency cap in `frontend/shared/uploadQueue.js` (`MAX_CONCURRENT_UPLOADS`, queues the rest) — CPU/network courtesy only, not a security boundary; the server limits are authoritative.
