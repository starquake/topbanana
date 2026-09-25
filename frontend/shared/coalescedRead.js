// createCoalescedRead wraps an async read so overlapping calls share one
// request plus at most one follow-up. A pending read older than staleMs is
// abandoned for a fresh one, so a hung request cannot block recovery; the
// caller's own sequence guard drops the abandoned read's late result.
export function createCoalescedRead(read, staleMs = 5000) {
    let pending = null;
    let startedAt = 0;
    let dirty = false;

    const run = () => {
        if (pending && Date.now() - startedAt < staleMs) {
            dirty = true;

            return pending;
        }
        const current = (async () => {
            try {
                do {
                    dirty = false;
                    startedAt = Date.now();
                    await read();
                } while (dirty && pending === current);
            } finally {
                if (pending === current) pending = null;
            }
        })();
        pending = current;

        return current;
    };

    return { run, pending: () => pending };
}
