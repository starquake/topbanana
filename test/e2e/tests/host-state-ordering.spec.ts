import type { APIRequestContext, Page } from '@playwright/test';

import { test, expect } from './fixtures';
import { importQuiz, setQuizMode, claimAndJoin, playerRow } from './helpers';

// #1178: GET /state reads fire un-awaited from every SSE tick with no
// sequencing, so two overlapping reads can resolve out of order and apply an
// older snapshot last - regressing the big screen from reveal back to question.
// The fix tags each read with a monotonically increasing id and ignores any
// response that is not the latest issued. This spec drives that race
// deterministically: it pins the surface on the reveal phase, closes the SSE
// stream so no background tick interferes, then fires two manual reads where the
// FIRST (older) returns a stale question snapshot on a delay and the SECOND
// (newer) returns the reveal snapshot immediately. The stale read therefore
// resolves last; the guard must drop it so the reveal survives.

type SessionState = {
  phase: string;
  serverNow: string;
  question: { id: number; startedAt: string | null; options: { id: number; text: string }[] } | null;
};

async function seedLiveQuiz(host: Page, title: string): Promise<void> {
  await importQuiz(host, {
    title,
    description: 'E2E host state-ordering quiz',
    timeLimitSeconds: 120,
    questions: [
      {
        text: 'What is 2+2?',
        options: [
          { text: '4', correct: true },
          { text: 'wrong-a', correct: false },
          { text: 'wrong-b', correct: false },
          { text: 'wrong-c', correct: false },
        ],
      },
    ],
  });
  setQuizMode(title, 'live');
}

async function joinAndReady(request: APIRequestContext, code: string, displayName: string): Promise<void> {
  await claimAndJoin(request, code, displayName);
  const readyResp = await request.post(`/api/sessions/${code}/ready`, { data: { ready: true } });
  expect(readyResp.status()).toBe(204);
}

// answerOverApi resolves the option id whose text matches off the participant's
// GET /state and POSTs it, waiting for the answer window to open first.
async function answerOverApi(request: APIRequestContext, code: string, text: string): Promise<void> {
  let optionId: number | undefined;
  await expect(async () => {
    const resp = await request.get(`/api/sessions/${code}/state`);
    expect(resp.ok(), `state read: ${resp.status()} ${await resp.text()}`).toBeTruthy();
    const state = (await resp.json()) as SessionState;
    expect(state.phase, 'expected the session to be in the question phase').toBe('question');
    expect(state.question?.startedAt, 'question should carry an answers-open anchor').toBeTruthy();
    expect(
      Date.parse(state.serverNow) >= Date.parse(state.question!.startedAt!),
      'answer window should have opened (read beat elapsed)',
    ).toBeTruthy();
    const option = state.question!.options.find((o) => o.text === text);
    expect(option, `option ${text} not found in question`).toBeTruthy();
    optionId = option!.id;
  }).toPass({ timeout: 15_000 });
  const answerResp = await request.post(`/api/sessions/${code}/answer`, { data: { optionId } });
  expect(answerResp.status(), `answer: ${await answerResp.text()}`).toBe(204);
}

// closeHostStream closes the big screen's SSE channel so no background tick
// fires refresh() during the controlled out-of-order reads.
async function closeHostStream(page: Page): Promise<boolean> {
  return page.evaluate(() => {
    const root = document.querySelector('[x-data^="hostBigScreen"]');
    const cmp = (window as unknown as {
      Alpine: { $data: (el: Element) => { source: EventSource | null } };
    }).Alpine.$data(root!);
    const source = cmp.source;
    if (!source) return false;
    source.close();
    return source.readyState === EventSource.CLOSED;
  });
}

test.describe('host big screen state ordering', () => {
  test('a stale /state response that resolves after a newer one does not regress reveal to question', async ({ hostSessions }) => {
    test.setTimeout(90_000);

    const stamp = Date.now();
    const quizTitle = `Host State Order ${stamp}`;
    const robin = `Robin-${stamp}`;
    const quincy = `Quincy-${stamp}`;

    const host = await hostSessions.adminHost();
    await seedLiveQuiz(host, quizTitle);

    const { joinCode } = await hostSessions.hostLive(quizTitle);
    expect(joinCode).toMatch(/^[A-Z0-9]{6}$/);

    const robinContext = await hostSessions.newPlayerContext();
    const quincyContext = await hostSessions.newPlayerContext();
    await joinAndReady(robinContext.request, joinCode, robin);
    await joinAndReady(quincyContext.request, joinCode, quincy);
    await expect(playerRow(host, robin)).toBeVisible({ timeout: 15_000 });
    await expect(playerRow(host, quincy)).toBeVisible();

    await host.getByRole('button', { name: 'Start now' }).click();
    await expect(host.locator('[data-phase-question]')).toBeVisible({ timeout: 20_000 });

    // Both players answer; the runner closes the question and reveals. The
    // correct option lights up - the reveal-phase signal the big screen exposes.
    await answerOverApi(robinContext.request, joinCode, '4');
    await answerOverApi(quincyContext.request, joinCode, 'wrong-a');
    const correctOption = host.locator('[data-answer-option][data-correct="true"]');
    await expect(correctOption).toHaveCount(1, { timeout: 20_000 });

    // Capture the authoritative reveal snapshot before intercepting, so both
    // fabricated responses share a valid body and only phase differs.
    const revealResp = await host.request.get(`/api/sessions/${joinCode}/state`);
    expect(revealResp.ok()).toBeTruthy();
    const revealBody = await revealResp.text();
    expect((JSON.parse(revealBody) as SessionState).phase).toBe('reveal');
    const staleBody = JSON.stringify({ ...JSON.parse(revealBody), phase: 'question' });

    // Silence the live stream so only the two manual reads below race. A closed
    // EventSource fires no ticks, so refresh() runs only when we call it.
    expect(await closeHostStream(host)).toBe(true);

    // Serve the FIRST /state read (older) the stale question body on a delay, and
    // the SECOND (newer) the reveal body immediately, so the stale one resolves
    // last. Later reads (none expected here) fall back to the reveal body.
    let stateCall = 0;
    await host.route(`**/api/sessions/${joinCode}/state`, async (route) => {
      const n = stateCall;
      stateCall += 1;
      if (n === 0) {
        await new Promise((resolve) => setTimeout(resolve, 1500));
        await route.fulfill({ status: 200, contentType: 'application/json', body: staleBody });
        return;
      }
      await route.fulfill({ status: 200, contentType: 'application/json', body: revealBody });
    });

    // Fire two overlapping reads: refresh #1 (seq 1) gets the delayed stale
    // question, refresh #2 (seq 2) gets the immediate reveal. Un-awaited, exactly
    // as an SSE tick issues them.
    await host.evaluate(() => {
      const root = document.querySelector('[x-data^="hostBigScreen"]');
      const cmp = (window as unknown as {
        Alpine: { $data: (el: Element) => { refresh: () => void } };
      }).Alpine.$data(root!);
      cmp.refresh();
      cmp.refresh();
    });

    // Let the stale (question) response resolve (~1.5s) and settle. With the
    // fetch-sequence guard the older read is dropped, so the correct-option
    // highlight survives; without it the stale question snapshot applied last and
    // the highlight would vanish.
    await host.waitForTimeout(2500);
    await expect(correctOption).toHaveCount(1);
  });
});
