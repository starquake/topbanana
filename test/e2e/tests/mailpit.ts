// Mailpit query helpers for the email round-trip specs. The worker
// servers send their mail (verify, invite, ...) to the shared mailpit
// catch-all (wired in playwright.config.ts); these helpers read it back
// over mailpit's HTTP API. The app sends mail asynchronously (a
// best-effort goroutine), so reads poll until the message lands.

// mailpitBaseURL derives the API base from TOPBANANA_E2E_MAILPIT, the
// "smtp,http" port pair playwright.config.ts publishes once per run.
export function mailpitBaseURL(): string {
  const cached = process.env.TOPBANANA_E2E_MAILPIT;
  if (!cached) {
    throw new Error('TOPBANANA_E2E_MAILPIT is not set; the mailpit helper cannot find the API');
  }
  const http = cached.split(',')[1];
  return `http://127.0.0.1:${http}`;
}

type MailpitSummary = { ID: string; To: { Address: string }[] };
type MailpitMessage = { Subject?: string; Text?: string; HTML?: string };

export type DeliveredEmail = { subject: string; text: string };

// waitForEmailLink polls mailpit for the newest message addressed to
// `recipient` and returns the first absolute URL whose path contains
// `pathContains` (e.g. "/verify-email?token="). One shared inbox serves
// all parallel workers, so filtering by the unique per-test recipient -
// not "latest message" - is what keeps the round-trip specs from picking
// up each other's mail.
export async function waitForEmailLink(
  recipient: string,
  pathContains: string,
  timeoutMs = 10_000,
): Promise<string> {
  const base = mailpitBaseURL();
  const deadline = Date.now() + timeoutMs;
  let lastErr = 'no matching message arrived';

  while (Date.now() < deadline) {
    // Scan every message addressed to the recipient, not just the
    // newest: a recipient can accumulate more than one mail (a retried
    // spec re-registers the same address, future flows may add others),
    // and not all of them carry the link this caller wants.
    const matches = await messagesTo(base, recipient);
    for (const m of matches) {
      const link = await linkFromMessage(base, m.ID, pathContains);
      if (link) {
        return link;
      }
    }
    if (matches.length > 0) {
      lastErr = `${matches.length} message(s) to ${recipient}, none carrying a link containing ${pathContains}`;
    }
    await sleep(150);
  }

  throw new Error(`waitForEmailLink(${recipient}, ${pathContains}) timed out: ${lastErr}`);
}

// waitForEmail polls mailpit for a message addressed to `recipient` and
// returns its subject and text body. Some notices (e.g. the role-change
// notice) carry no link, so unlike waitForEmailLink this returns the
// message content for the caller to assert on. Filters by the unique
// per-test recipient so parallel workers do not cross-read the shared
// inbox.
export async function waitForEmail(recipient: string, timeoutMs = 10_000): Promise<DeliveredEmail> {
  const base = mailpitBaseURL();
  const deadline = Date.now() + timeoutMs;

  while (Date.now() < deadline) {
    const matches = await messagesTo(base, recipient);
    for (const m of matches) {
      const body = await messageBody(base, m.ID);
      if (body) {
        return body;
      }
    }
    await sleep(150);
  }

  throw new Error(`waitForEmail(${recipient}) timed out: no message arrived`);
}

async function messageBody(base: string, id: string): Promise<DeliveredEmail | undefined> {
  const res = await fetch(`${base}/api/v1/message/${id}`);
  if (!res.ok) {
    return undefined;
  }
  const body = (await res.json()) as MailpitMessage;
  return { subject: body.Subject ?? '', text: body.Text ?? body.HTML ?? '' };
}

async function messagesTo(base: string, recipient: string): Promise<MailpitSummary[]> {
  const url = `${base}/api/v1/search?query=${encodeURIComponent(`to:${recipient}`)}&limit=20`;
  const res = await fetch(url);
  if (!res.ok) {
    return [];
  }
  const data = (await res.json()) as { messages?: MailpitSummary[] };
  // The search tokenises the address, so confirm an exact To match
  // before trusting each hit.
  return (data.messages ?? []).filter((m) =>
    m.To?.some((t) => t.Address.toLowerCase() === recipient.toLowerCase()),
  );
}

async function linkFromMessage(base: string, id: string, pathContains: string): Promise<string | undefined> {
  const res = await fetch(`${base}/api/v1/message/${id}`);
  if (!res.ok) {
    return undefined;
  }
  const body = (await res.json()) as MailpitMessage;
  const text = body.Text ?? body.HTML ?? '';
  const re = new RegExp(`https?://[^\\s"'<>]*${escapeRegExp(pathContains)}[^\\s"'<>]*`);
  return re.exec(text)?.[0];
}

function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
