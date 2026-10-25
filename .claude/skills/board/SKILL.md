---
name: board
description: >
  Short alias for the board loop. Use when the maintainer types "/board",
  "board", "board loop", or "run the board": it starts `work-the-board`'s LOOP
  form (a triage-and-advance pass over topbanana's board on a schedule, with
  the persistent monitor armed). "/board once" runs a single pass; "/board
  stop" (or "stop the board") ends it. Trigger on the bare word even without
  "skill".
---

This is an **alias**, not a second workflow. Everything about how a pass
behaves (the gates it stops at, the one-build cap, `hold`, the Next-steps
comment) lives in `work-the-board`. Read that skill and follow it; this file
only decides *which form* to run and *with what defaults*.

| They type | You run |
|---|---|
| `/board`, `board`, `run the board` | **Loop form, 60-minute interval, monitor armed** |
| `/board 30m`, `board every 30m` | Loop form at that interval, monitor armed |
| `/board once`, `work the board` | **One pass**, no monitor |
| `/board stop`, `stop the board` | End the loop **and** stop the monitor |

**60 minutes is the default on purpose.** The monitor makes the loop react to
a maintainer comment, a label or a card move within about a minute; the timed
tick is only a backstop for things no event announces.

## Starting a loop session

1. **Check a monitor isn't already running** for this board. Two monitors mean
   two notifications per event.
2. **Arm the monitor**, persistent, exactly as `work-the-board` specifies: all
   the signals, including comments and board Status moves. Don't hand-roll a
   trimmed version: a watch without comments drops the maintainer's answers,
   and one without Status misses a card moved to `Build`.
3. **Run the pass**, then schedule the next tick.

## Stopping

`/board stop` does **both**: end the schedule and stop the monitor. Stopping
only the loop leaves a watch emitting events with nothing driving them.
