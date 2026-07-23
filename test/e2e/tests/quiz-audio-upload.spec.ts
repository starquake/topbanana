import { test, expect } from './fixtures';
import {
  createQuizWithQuestions,
  openMediaPicker,
  type QuestionSpec,
} from './helpers';
import { adminStatePath } from '../e2e-auth';

test.use({ storageState: adminStatePath() });

// makeWav builds a minimal but real WAV (PCM, mono, 8-bit) of the given length
// in seconds. The browser decodes it for the loadedmetadata duration read, and
// the server sniffs the "RIFF"/"WAVE" magic bytes to accept it - so a single
// fixture exercises both halves of the upload path.
function makeWav(seconds: number): Buffer {
  const sampleRate = 8000;
  const numSamples = Math.round(sampleRate * seconds);
  const dataSize = numSamples; // 1 byte per sample (8-bit PCM).
  const buf = Buffer.alloc(44 + dataSize);
  buf.write('RIFF', 0);
  buf.writeUInt32LE(36 + dataSize, 4);
  buf.write('WAVE', 8);
  buf.write('fmt ', 12);
  buf.writeUInt32LE(16, 16); // fmt chunk size
  buf.writeUInt16LE(1, 20); // PCM
  buf.writeUInt16LE(1, 22); // mono
  buf.writeUInt32LE(sampleRate, 24);
  buf.writeUInt32LE(sampleRate, 28); // byte rate
  buf.writeUInt16LE(1, 32); // block align
  buf.writeUInt16LE(8, 34); // bits per sample
  buf.write('data', 36);
  buf.writeUInt32LE(dataSize, 40);
  buf.fill(128, 44); // silence (8-bit PCM midpoint)

  return buf;
}

const QUESTIONS: readonly QuestionSpec[] = [
  { text: 'Name that sound', options: ['a', 'b', 'c', 'd'], correctIndices: [0] },
];

test('uploading a sound adds it to the library with a duration and an audio preview', async ({ page, browserName }) => {
  test.setTimeout(60_000);

  const quizTitle = `E2E Audio Upload ${browserName}`;
  await createQuizWithQuestions(page, quizTitle, QUESTIONS);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  await page.locator('input[type="file"][name="audio"]').setInputFiles({
    name: 'clip.wav',
    mimeType: 'audio/wav',
    buffer: makeWav(1),
  });

  // The JS reloads to the audio section once the clip lands.
  const libraryItem = page.getByTestId('audio-library-item');
  await expect(libraryItem).toHaveCount(1, { timeout: 45_000 });

  // The in-browser duration read surfaces a M:SS label.
  await expect(page.getByTestId('audio-duration').first()).toHaveText('0:01');

  // The inline preview points at the served audio.
  await expect(libraryItem.locator('audio')).toHaveAttribute('src', /\/media\/\d+$/);
});

test('an uploaded sound defaults its description to the filename and can be edited inline', async ({
  page,
  browserName,
}) => {
  test.setTimeout(60_000);

  const quizTitle = `E2E Audio Description ${browserName}`;
  await createQuizWithQuestions(page, quizTitle, QUESTIONS);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  await page.locator('input[type="file"][name="audio"]').setInputFiles({
    name: 'Opening Theme.wav',
    mimeType: 'audio/wav',
    buffer: makeWav(1),
  });
  await expect(page.getByTestId('audio-library-item')).toHaveCount(1, { timeout: 45_000 });

  // The description defaults to the filename without its extension.
  const descInput = page.getByTestId('audio-description-input').first();
  await expect(descInput).toHaveValue('Opening Theme');

  // Edit the label inline; the htmx swap re-renders the form with the saved value.
  await descInput.fill('Round one intro');
  await page.getByTestId('audio-description-save').first().click();
  await expect(page.getByTestId('audio-description-input').first()).toHaveValue('Round one intro');

  // A reload proves it persisted, not just swapped in the DOM.
  await page.reload();
  await expect(page.getByTestId('audio-description-input').first()).toHaveValue('Round one intro');
});

test('an edited sound description shows in the question editor audio picker', async ({ page, browserName }) => {
  test.setTimeout(60_000);

  const quizTitle = `E2E Audio Picker Desc ${browserName}`;
  await createQuizWithQuestions(page, quizTitle, QUESTIONS);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  await page.locator('input[type="file"][name="audio"]').setInputFiles({
    name: 'jingle.wav',
    mimeType: 'audio/wav',
    buffer: makeWav(2),
  });
  await expect(page.getByTestId('audio-library-item')).toHaveCount(1, { timeout: 45_000 });

  const descInput = page.getByTestId('audio-description-input').first();
  await descInput.fill('Theme tune');
  await page.getByTestId('audio-description-save').first().click();
  await expect(page.getByTestId('audio-description-input').first()).toHaveValue('Theme tune');

  // Editing happens in the editor now (#1260); open it and select the question.
  await page.getByTestId('open-question-editor').click();
  await page.locator('article.q-row').first().click();
  await expect(page.locator('#question-editor form')).toBeVisible();

  await openMediaPicker(page, 'audio');
  const picker = page.getByTestId('question-audio-picker');
  await expect(picker).toBeVisible();
  await expect(picker.getByTestId('audio-library-item').first()).toContainText('Theme tune');
});

test('the question editor audio picker lists a sound and attaches it', async ({ page, browserName }) => {
  test.setTimeout(60_000);

  const quizTitle = `E2E Audio Picker ${browserName}`;
  await createQuizWithQuestions(page, quizTitle, QUESTIONS);
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+$/);

  await page.locator('input[type="file"][name="audio"]').setInputFiles({
    name: 'pick.wav',
    mimeType: 'audio/wav',
    buffer: makeWav(2),
  });
  await expect(page.getByTestId('audio-library-item')).toHaveCount(1, { timeout: 45_000 });

  // Open the question editor for the one seeded question.
  // Editing happens in the editor now (#1260); open it and select the question.
  await page.getByTestId('open-question-editor').click();
  await page.locator('article.q-row').first().click();
  await expect(page.locator('#question-editor form')).toBeVisible();

  await openMediaPicker(page, 'audio');
  const picker = page.getByTestId('question-audio-picker');
  await expect(picker).toBeVisible();
  await expect(picker.getByTestId('audio-duration').first()).toHaveText('0:02');

  // Select the audio and save. The radio is visually hidden (sr-only) behind
  // its styled label, so check it directly rather than clicking the label,
  // whose centre overlaps the inline audio control.
  await picker
    .getByTestId('audio-library-item')
    .first()
    .locator('input[name="audio_media_id"]')
    .check({ force: true });
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  // Saving from the pane stays on the page now (#1244 slice 2) rather than
  // redirecting to the quiz view; the rail row picks up the audio flag.
  await expect(page).toHaveURL(/\/admin\/quizzes\/\d+\/questions/);
  await expect(page.getByTestId('q-badge-audio').first()).toBeVisible();

  // Reload from the deep link; the audio's radio is still checked.
  await page.reload();
  await expect(page.locator('#question-editor form')).toBeVisible();
  await openMediaPicker(page, 'audio');
  const chosen = page
    .getByTestId('question-audio-picker')
    .getByTestId('audio-library-item')
    .first()
    .locator('input[name="audio_media_id"]');
  await expect(chosen).toBeChecked();
});
