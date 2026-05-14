// SSE-driven live refresh. When the server broadcasts atx_machine_changed,
// replace <main>'s contents with a freshly-fetched version of the current
// page, so machine cards / window lists update without a full reload.

(function () {
    const MAIN_ID = 'atx-main';
    const DEBOUNCE_MS = 200;

    let pending = null;

    function refresh() {
        if (pending) return;
        pending = setTimeout(async () => {
            pending = null;
            try {
                const resp = await fetch(location.pathname + location.search, {
                    headers: { 'X-Atx-Refresh': '1' },
                    cache: 'no-store',
                });
                if (!resp.ok) return;
                const html = await resp.text();
                const doc = new DOMParser().parseFromString(html, 'text/html');
                const fresh = doc.getElementById(MAIN_ID);
                const current = document.getElementById(MAIN_ID);
                if (fresh && current) current.innerHTML = fresh.innerHTML;
            } catch (_) {
                // Ignore — the next event will retry.
            }
        }, DEBOUNCE_MS);
    }

    function shouldRefreshFor(machineName) {
        // Machine list page: always refresh.
        if (location.pathname === '/atx/' || location.pathname === '/atx') return true;
        // Per-machine page: only if the event is for this machine.
        const m = location.pathname.match(/^\/atx\/m\/([^/]+)/);
        return !!m && m[1] === machineName;
    }

    const es = new EventSource('/events');
    es.addEventListener('atx_machine_changed', (e) => {
        if (shouldRefreshFor(e.data)) refresh();
    });
})();
