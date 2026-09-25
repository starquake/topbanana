// createCoalescedRead wraps an async read so overlapping calls share one
// request plus at most one follow-up. A pending read older than staleMs is
// abandoned for a fresh one, so a hung request cannot block recovery. The
// abandoned read's AbortSignal fires so its request frees its socket; the
// caller's own sequence guard drops any late result.
export function createCoalescedRead(read, staleMs = 5000) {
    let pending = null;
    let startedAt = 0;
    let dirty = false;
    let controller = null;

    const run = () => {
        if (pending && Date.now() - startedAt < staleMs) {
            dirty = true;

            return pending;
        }
        if (controller) controller.abort();
        const current = (async () => {
            let failed = false;
            let failure;
            do {
                dirty = false;
                startedAt = Date.now();
                const own = new AbortController();
                controller = own;
                try {
                    await read(own.signal);
                } catch (err) {
                    // Keep going so a queued follow-up still runs; report after.
                    if (!own.signal.aborted) {
                        failed = true;
                        failure = err;
                    }
                }
                if (controller === own) controller = null;
            } while (dirty && pending === current);
            if (pending === current) pending = null;
            if (failed) throw failure;
        })();
        pending = current;

        return current;
    };

    return { run, pending: () => pending };
}
