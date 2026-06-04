# Multiplayer Milestone - Plan

Planning document for the hosted, synchronized "quiz night" mode. This is a
proposal for how to slice the work into reviewable, testable tickets. The
tickets here are now tracked as GitHub issues in the "Multiplayer Launch"
milestone (MP-0..MP-10 = #677-#687); each ticket below links to its issue.

## The game loop (target)

One host opens a quiz and puts it on a TV. The TV shows a lobby with a join QR
code and a short room code. Players join from their phones (QR) or a PC (typed
code) with just a display name. The host can also join as a player on their own
phone. Joining players appear on the TV live. Players mark themselves ready;
once everyone has been ready for a short while the quiz auto-starts, and the
host can override and start early. On start, the first round is shown and
auto-advances to its first question. Everyone gets the same time to answer.
As players answer, a badge per player appears at the bottom in answer order -
without revealing correctness. When everyone has answered (or time runs out)
that is shown, the correct answer is revealed, and after a brief beat the next
question appears. Between rounds the standings are shown as a bar graph that
animates the round's points being added and re-sorts the leaders to the top.
Then the next round begins, and so on, to a final standings screen.

## Locked decisions

Confirmed with the product owner:

1. **Scoring**: reuse the existing speed-based scoring (faster correct answer =
   more points via linear decay; 0 for wrong/late), the same formula solo play
   uses (`Service.CalculateScore`).
2. **Host model**: the host opens the session on their own authenticated device
   (PC/TV); that surface is BOTH the presentation screen and the host control
   panel (start/override, skip). The host may separately join as a player on
   their phone.
3. **Scope**: hosted live mode is NEW and coexists with the current solo
   self-paced client (both kept). A session has its own standings AND records
   its results into the quiz's existing leaderboard.
4. **Auth + join**: the host must be a logged-in host/admin and can host any
   quiz they can view. Players join anonymously (display name only, no account)
   via the QR or a typed room code.
5. **Quiz play mode**: every quiz is exclusively `solo` OR `live` (a new quiz
   field; no "both"). A `live` quiz is never solo-playable - so it cannot be
   pre-played and spoiled before a hosted game - and a `solo` quiz cannot be
   hosted. There is no existing quiz "type" field today; the only quiz enum is
   `visibility` (public/unlisted/private), which is about discoverability and
   stays orthogonal. Existing quizzes backfill to `solo`. (Mode is editable, but
   the no-spoiler guarantee only holds for quizzes that were `live` from
   creation.)

## Architecture and technology

Reuse the current stack end to end: Go (no new web framework), SQLite via sqlc,
the Alpine + plain-JS SPA, anime.js for animation, and the existing SSE hub
pattern. No WebSockets.

### Why the current game model is not enough

The existing game is **self-paced**: each player has their own `Game` row and
calls `GetNext` to advance at their own speed, with per-player question clocks.
Hosted multiplayer is the opposite: **one shared, server-paced session** that
everyone follows on a single clock, with a host. So multiplayer is a new
subsystem layered over the existing quiz/round/question/scoring code, not a
tweak to the solo flow.

### REST-first, SSE as a pure side-channel

This matches the requested design and the existing leaderboard precedent
(`leaderboard.Hub`: `Subscribe`/`Publish`).

- **All mutations are REST POSTs**: create session, join, set-ready,
  start/override, submit-answer, host-skip.
- **All reads are one REST endpoint**: `GET /api/sessions/{code}/state` returns
  the full authoritative state - phase, players + ready flags, current
  round/question, server `now` + phase `deadline`, answered-order, scores.
- **SSE carries no game data**: a per-session channel emits a tiny tick
  (`{version, phase}`) on every state change; each surface responds by
  re-GETting state. This doubles as the reconnect path - a dropped client just
  resubscribes and re-GETs.
- **Countdowns are client-side off server timestamps**: the server sends `now`
  and `deadline`; clients render the local ticking bar (the same technique the
  solo client already uses for the reveal/answer bars). SSE therefore only
  fires on phase transitions, never every second.

### The session state machine

Server-authoritative phases:

```
lobby -> (round_intro -> question -> reveal)* per round
      -> round_results (between rounds) -> ... -> finished
```

### The session runner (the one genuinely new building block)

A background runner advances phases on server timers (close a question on
timeout, reveal -> next question after a beat, auto-start once everyone has been
ready a while) and publishes a tick on each transition. The app already spawns
background goroutines (token sweep, retention sweep), so this fits an
established pattern. It holds in-memory state backed by the DB. The clock and
beats are injectable for tests, mirroring `SetRevealDelay`.

### Data model

New sqlc tables (names indicative):

- `sessions`: id, quiz_id, host_player_id, join_code (unique), phase,
  current_round_id, current_question_id, question_started_at,
  question_expires_at, created_at, started_at, finished_at.
- `session_players`: session_id, player_id, display_name, is_ready, joined_at,
  left_at, last_seen_at.
- `session_answers`: session_id, question_id, player_id, option_id,
  answered_at, score (or reuse the existing answers path - decided per MP-6).

Joiners reuse the anonymous-player model (display name -> petname/claim
patterns). Scoring reuses `CalculateScore`.

### Frontend surfaces

Three views, all driven by SSE-tick -> GET state:

- **TV / presentation + host control** (authenticated host device): lobby with
  QR + code, in-game question + answered badges + reveal, between-round bar
  graph, plus host controls.
- **Player** (phone/PC, anonymous): join + lobby + ready, then synchronized
  question play, reveal, and the standings bar graph.

### Feasibility and risks

Feasible on the current stack. Honest risks, all tractable:

- **Single instance**: the in-memory runner assumes one server process (the
  deploy is single-instance). Multi-instance would need shared pub/sub - out of
  scope.
- **Server restart mid-game**: persist phase + deadline so the runner can resume
  (handled in MP-10; acceptable to degrade in earlier tickets).
- **Clock skew**: send `deadline - serverNow` so clients never depend on their
  own wall clock being correct.
- **"All answered" with disconnects**: needs a definition of "active player"
  (last-seen heartbeat) so a dropped player does not stall the question
  (MP-10).
- **QR**: a small server-side SVG generator (or a tiny vendored lib), self
  hosted like the other assets.

## Assumptions / smaller defaults (change if wrong)

These are baked into the tickets below unless you say otherwise:

- **No late join** in v1: the lobby closes at start. Reconnect/resync is always
  allowed.
- **Auto-start window** (all-ready-for-N-seconds) is configurable; host override
  is always available.
- **A question closes early** when all active players have answered, otherwise on
  timeout.
- **Spectators** (watch without playing) are out of scope for v1.
- **Target scale**: a quiz-night room (tens of players), single instance.
- **The TV/presentation view is reachable via the session code** and is a
  presentation surface (never shows correctness during answering).

## Open questions (non-blocking; reasonable defaults assumed above)

- Auto-start window length and whether it shows a visible countdown on the TV.
- Should there be a per-question "host skip/pause" beyond start-override?
- Max players cap / behavior when a huge room joins.
- On host disconnect: pause the session, or end it?

## Tickets

Sequenced in four phases. Phase A delivers a fully working, demoable lobby;
Phase B+C make it playable; Phase D hardens it. Each ticket is sized to be
independently reviewable and end-to-end testable.

### Phase 0 - Foundation

**MP-0: Quiz play mode (solo | live) (backend + admin form)** - [#677](https://github.com/starquake/topbanana/issues/677)
- Goal: every quiz is explicitly either a solo quiz or a live quiz, so a live
  quiz can never be pre-played solo (no spoilers) and a solo quiz can't be
  hosted live.
- Scope: add `mode` to quizzes (`TEXT NOT NULL DEFAULT 'solo' CHECK (mode IN
  ('solo','live'))`), backfill existing rows to `solo`; quiz domain + sqlc +
  the create/edit admin form control to pick the mode; gate the solo browse +
  play paths to `mode='solo'` (live quizzes are neither listed nor
  solo-playable); the hosted create-session path (MP-1) accepts only
  `mode='live'`.
- Testing: integration - a live quiz is absent from the solo browse list and
  rejected by the solo play / game-create path; a solo quiz is rejected by
  hosted create-session (once MP-1 lands); the admin form round-trips the mode;
  existing quizzes read back as `solo`.
- Out of scope: the hosted session itself (MP-1+).
- Depends on: nothing. Foundational - MP-1 relies on the `live` gate.

### Phase A - Lobby (end to end)

**MP-1: Hosted session model + lobby REST API (backend)** - [#678](https://github.com/starquake/topbanana/issues/678)
- Goal: a logged-in host creates a live session for any visible quiz; players
  join anonymously and toggle ready; one endpoint returns the lobby state.
- Scope: `sessions` + `session_players` tables + sqlc; session domain type +
  phase enum (just `lobby` for now); collision-checked short join-code
  generator (ambiguity-free alphabet); REST `POST /api/sessions` (host-authed;
  quiz must be visible to them AND `mode='live'` per MP-0) -> code, `POST
  /api/sessions/{code}/join` (anonymous,
  display name), `POST /api/sessions/{code}/ready`, `GET
  /api/sessions/{code}/state` (phase + players + ready flags + quiz meta).
  Authz: create/host gated to host/admin; join/ready/state for participants.
- Testing: integration (real DB + HTTP) - create, multiple joins (display-name
  collisions fall back to petname), ready toggles reflected in state, join-code
  uniqueness, authz (non-host cannot create; invisible quiz rejected; non-`live`
  quiz rejected).
- Out of scope: SSE, timing, gameplay.
- Depends on: MP-0 (the `live`-mode gate).

**MP-2: Session event channel (SSE side-channel) (backend)** - [#679](https://github.com/starquake/topbanana/issues/679)
- Goal: every surface is notified the instant session state changes, and
  reconnects cleanly, without SSE carrying game data.
- Scope: a per-session hub mirroring `leaderboard.Hub`; `GET
  /api/sessions/{code}/events` SSE endpoint emitting `{version, phase}` ticks +
  heartbeats (reuse the leaderboard SSE handler's flush/heartbeat/write-deadline
  handling); publish on the Phase-A mutations; document the tick -> GET-state
  contract.
- Testing: integration - subscriber receives a tick on join/ready/leave;
  heartbeat frames; multiple subscribers; version increments; unsubscribe on
  disconnect.
- Out of scope: frontend.
- Depends on: MP-1.

**MP-3: Host presentation + lobby + QR + start control (frontend)** - [#680](https://github.com/starquake/topbanana/issues/680)
- Goal: a host clicks "Play live" on a quiz and gets a TV-ready lobby with the
  join QR + room code, sees players appear and ready-up live, and can start.
- Scope: new host/presentation route + view; "Play live" entry from the quiz
  admin page; server-rendered QR (SVG) of the join URL + the room code; live
  player list with ready states (SSE -> GET); host Start (override) control
  (auto-start wiring lands in MP-5); TV-resolution layout using the design
  system.
- Testing: e2e - host creates a session; a player joins in a second context;
  the TV lobby shows them and their ready state live. Demoable end to end with
  MP-4.
- Out of scope: in-game screens.
- Depends on: MP-1, MP-2.

**MP-4: Player join + lobby + ready (frontend)** - [#681](https://github.com/starquake/topbanana/issues/681)
- Goal: a player scans the QR or types the code on a PC, enters a display name,
  lands in the lobby, and toggles ready - updating the TV live.
- Scope: join route (QR deep-link target + a "enter code" form for PC);
  display-name entry (reuse claim-name/petname); lobby view (who's here + my
  ready toggle); host-as-player works by opening this on a phone.
- Testing: e2e - full lobby loop with MP-3: join via code, ready, see it on the
  TV; PC join via typed code.
- Depends on: MP-1, MP-2.

_End of Phase A: the lobby works and is demoable end to end._

### Phase B - Synchronized engine (backend)

**MP-5: Session runner - start + round/question loop + answering + reveal (backend)** - [#682](https://github.com/starquake/topbanana/issues/682)
- Goal: a started session marches itself through rounds and questions on a
  server clock - issuing each question with a synced deadline, accepting
  answers, closing on all-answered-or-timeout, and revealing the correct
  answer - all observable via GET state + SSE ticks.
- Scope: the background session runner (fits the existing sweep-goroutine
  pattern) owning phase transitions and timers, with an injectable clock/beats
  for tests (mirror `SetRevealDelay`); auto-start once all joined players have
  been ready for the configurable window, with host Start overriding
  immediately; phases round_intro (reuse #548 round intro) -> question (server
  `StartedAt`/`ExpiredAt`, reuse the reveal beat) -> reveal (correct answer)
  -> next; `POST /api/sessions/{code}/answer` recording a pick, scored at close
  with the existing `CalculateScore`; close early when all active players have
  answered, else on timeout; answered-order tracking in state (never
  correctness pre-reveal); publish on every transition.
- Testing: integration with an injected fast clock - drive a session from start
  through several questions; assert phase order, per-question deadlines,
  answered-order, correctness absent pre-reveal and present at reveal, scores
  via the formula, and both early-close (all answered) and timeout-close paths.
- Out of scope: round-results/standings (MP-6); all frontend.
- Depends on: MP-1, MP-2.
- Note: this is the largest backend ticket but it is one coherent testable unit
  (a question runs and reveals). If it proves too big in practice, the natural
  seam is "start + question issue/answer/close" vs "reveal beat"; prefer keeping
  them together.

**MP-6: Round results, final standings + leaderboard recording (backend)** - [#683](https://github.com/starquake/topbanana/issues/683)
- Goal: after each round the session exposes per-player round deltas + running
  totals (for the bar graph), and on finish produces final standings and records
  the result into the quiz's existing leaderboard.
- Scope: round_results phase between rounds (per-player points-this-round + new
  totals + ranking in state); finished phase (final standings); record the
  session's per-player results into the existing leaderboard/answers path so the
  quiz's standard leaderboard reflects the live game (decision 3).
- Testing: integration - run a multi-round session; assert round deltas +
  cumulative totals + ordering at each round_results, the final standings, and
  that the existing quiz-leaderboard query returns the recorded results.
- Depends on: MP-5.

### Phase C - Gameplay frontend

**MP-7: Player in-game play (frontend)** - [#684](https://github.com/starquake/topbanana/issues/684)
- Goal: a player plays a synchronized question - question with a countdown
  synced to the server deadline, one answer submit, then a "waiting" state (no
  correctness), then the revealed correct answer.
- Scope: player in-game views off GET state + SSE - round_intro screen, question
  + client-local countdown (`deadline - serverNow`), answer submit + "answered,
  waiting", reveal (correct answer). Reuse the solo client's countdown/feedback
  patterns.
- Testing: e2e - host on one context, player on another; player answers, sees
  waiting then the correct answer; countdown driven by Playwright's clock.
- Depends on: MP-5 (and MP-3/MP-4 for setup).

**MP-8: TV in-game presentation (frontend)** - [#685](https://github.com/starquake/topbanana/issues/685)
- Goal: the TV shows the live question with a countdown and the answer-order
  badges filling in, then the all-answered/reveal state with the correct
  answer, then advances.
- Scope: TV in-game view - question + countdown, answered badges in answer order
  (no correctness), all-answered indicator, reveal of the correct answer,
  auto-advance.
- Testing: e2e - drive a session; assert badges appear in answer order,
  correctness hidden until reveal, and the advance.
- Depends on: MP-5.

**MP-9: Round score bar-graph animation (TV + player)** - [#686](https://github.com/starquake/topbanana/issues/686)
- Goal: between rounds, a bar graph of standings animates the round's points
  being added and re-sorts the leaders to the top.
- Scope: round_results screen consuming MP-6's deltas + totals; anime.js bar
  graph that starts from pre-round totals, animates the round delta, and
  re-sorts; reduced-motion respected.
- Testing: e2e - assert the post-animation standings order + values; eyeball /
  screenshot the animation.
- Depends on: MP-6, MP-8.

### Phase D - Robustness

**MP-10: Reconnection, disconnects, and lifecycle edges** - [#687](https://github.com/starquake/topbanana/issues/687)
- Goal: the live game survives dropped connections and messy real-world states.
- Scope: SSE reconnect resync (player/TV/host re-GET state and land on the right
  phase via deadlines); "active player" definition for all-answered
  (heartbeat/last-seen) so a dropped player does not stall a question; player
  leave updating badges/standings; host-disconnect / abandon-session handling +
  cleanup; optional server-restart resume from persisted phase + deadline;
  enforce "lobby closes at start" (no late join in v1).
- Testing: integration + e2e - drop and reconnect mid-question and resync; a
  non-answering disconnected player does not stall all-answered; leave updates
  standings; abandoned-session cleanup.
- Depends on: MP-5, MP-6, and the Phase-C surfaces.
