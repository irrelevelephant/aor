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

    function shouldRefreshFor(_machineName) {
        // The unified view at /atx/ shows all machines; refresh on any
        // event. The terminal page must NOT refresh — wiping <main>
        // would destroy the live xterm.js DOM.
        return location.pathname === '/atx/' || location.pathname === '/atx';
    }

    const es = new EventSource('/events');
    es.addEventListener('atx_machine_changed', (e) => {
        if (shouldRefreshFor(e.data)) refresh();
    });

    // --- Web Push subscription ---
    // If notification permission is already granted, make sure the server
    // has our current subscription endpoint registered. We don't prompt
    // here — permission is requested elsewhere (e.g. a user-initiated tap
    // on an "Enable notifications" affordance, future).

    async function ensurePushSubscription() {
        if (!('serviceWorker' in navigator) || !('PushManager' in window)) return;
        if (Notification.permission !== 'granted') return;
        try {
            const reg = await navigator.serviceWorker.ready;
            let sub = await reg.pushManager.getSubscription();
            if (!sub) {
                const r = await fetch('/atx/api/push/vapid-public-key');
                if (!r.ok) return;
                const { key } = await r.json();
                sub = await reg.pushManager.subscribe({
                    userVisibleOnly: true,
                    applicationServerKey: urlBase64ToUint8Array(key),
                });
            }
            const j = sub.toJSON();
            await fetch('/atx/api/push/subscribe', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    endpoint: j.endpoint,
                    keys: j.keys,
                    user_agent: navigator.userAgent,
                }),
            });
        } catch (_) { /* push isn't critical; ignore failures */ }
    }

    function urlBase64ToUint8Array(s) {
        const pad = '='.repeat((4 - (s.length % 4)) % 4);
        const b64 = (s + pad).replace(/-/g, '+').replace(/_/g, '/');
        const raw = atob(b64);
        const out = new Uint8Array(raw.length);
        for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
        return out;
    }

    async function requestPushPermission() {
        if (!('Notification' in window)) return 'unsupported';
        if (Notification.permission === 'granted') {
            await ensurePushSubscription();
            return 'granted';
        }
        if (Notification.permission === 'denied') return 'denied';
        const perm = await Notification.requestPermission();
        if (perm === 'granted') await ensurePushSubscription();
        return perm;
    }

    function syncPushToggleButton() {
        const btn = document.getElementById('atx-push-toggle');
        if (!btn) return;
        if (!('Notification' in window) || !('serviceWorker' in navigator) || !('PushManager' in window)) {
            btn.hidden = true;
            return;
        }
        // Only show when permission is in the can-still-prompt state.
        btn.hidden = Notification.permission !== 'default';
    }

    document.addEventListener('click', async (e) => {
        const btn = e.target.closest('#atx-push-toggle');
        if (!btn) return;
        btn.disabled = true;
        const result = await requestPushPermission();
        btn.disabled = false;
        if (result === 'denied') {
            btn.textContent = '🔕 Notifications blocked';
            btn.title = 'Re-enable in your browser/system notification settings';
        } else if (result === 'granted') {
            btn.hidden = true;
        }
    });

    // Backwards-compatible global for console / future affordances.
    window.atxRequestPushPermission = requestPushPermission;

    // --- unified machine nav (root view) ---
    // Header click toggles a machine's window list. First-time expand
    // lazy-fetches windows via the JSON API. Expanded set is persisted to
    // both localStorage (for the JS to read after innerHTML refresh) and
    // a cookie (so the server can eager-render those machines next load).

    const EXPANDED_KEY = 'atx.expanded';
    const LAST_WINDOW_KEY = 'atx.lastWindow';

    function readExpanded() {
        try {
            const arr = JSON.parse(localStorage.getItem(EXPANDED_KEY) || '[]');
            return new Set(Array.isArray(arr) ? arr : []);
        } catch (_) { return new Set(); }
    }

    function writeExpanded(set) {
        const arr = Array.from(set);
        try { localStorage.setItem(EXPANDED_KEY, JSON.stringify(arr)); } catch (_) {}
        document.cookie = 'atx_expanded=' + encodeURIComponent(arr.join(',')) +
            '; path=/atx/; max-age=31536000; samesite=lax';
    }

    async function loadWindows(machine, container) {
        try {
            const resp = await fetch('/atx/api/m/' + encodeURIComponent(machine) + '/windows', { cache: 'no-store' });
            if (!resp.ok) return;
            container.innerHTML = await resp.text();
        } catch (_) { /* leave empty; next toggle retries */ }
    }

    function highlightLastWindow() {
        let last;
        try { last = JSON.parse(localStorage.getItem(LAST_WINDOW_KEY) || 'null'); } catch (_) {}
        if (!last || !last.machine) return;
        const row = document.querySelector(
            '.window-row[data-machine="' + CSS.escape(last.machine) + '"]' +
            '[data-window="' + Number(last.window) + '"]'
        );
        if (!row) return;
        row.classList.add('last-used');
        row.scrollIntoView({ block: 'center', behavior: 'auto' });
        setTimeout(() => row.classList.remove('last-used'), 1800);
    }

    function setMachineExpanded(li, expand) {
        const machine = li.dataset.machine;
        const container = document.getElementById('w-' + machine);
        const header = li.querySelector('.machine-header');
        if (expand) {
            li.dataset.expanded = '1';
            container.hidden = false;
            header.setAttribute('aria-expanded', 'true');
            if (!container.dataset.loaded) {
                container.dataset.loaded = '1';
                loadWindows(machine, container);
            }
        } else {
            li.dataset.expanded = '';
            container.hidden = true;
            header.setAttribute('aria-expanded', 'false');
        }
    }

    function allMachines() {
        return document.querySelectorAll('.machine');
    }

    function allExpanded() {
        const machines = allMachines();
        if (machines.length === 0) return false;
        for (const li of machines) {
            if (li.dataset.expanded !== '1') return false;
        }
        return true;
    }

    function syncExpandAllButton() {
        const btn = document.getElementById('expand-all');
        if (!btn) return;
        const open = allExpanded();
        const label = open ? 'Collapse all' : 'Expand all';
        btn.setAttribute('aria-label', label);
        btn.title = label;
    }

    function setAllExpanded(expand) {
        const set = readExpanded();
        for (const li of allMachines()) {
            setMachineExpanded(li, expand);
            if (expand) set.add(li.dataset.machine);
            else set.delete(li.dataset.machine);
        }
        writeExpanded(set);
        syncExpandAllButton();
    }

    document.addEventListener('click', (e) => {
        const header = e.target.closest('.machine-header');
        if (header) {
            const li = header.closest('.machine');
            const expand = li.dataset.expanded !== '1';
            setMachineExpanded(li, expand);
            const set = readExpanded();
            if (expand) set.add(li.dataset.machine);
            else set.delete(li.dataset.machine);
            writeExpanded(set);
            syncExpandAllButton();
            return;
        }
        if (e.target.closest('#expand-all')) {
            setAllExpanded(!allExpanded());
            return;
        }
        const row = e.target.closest('.window-row');
        if (row) {
            try {
                localStorage.setItem(LAST_WINDOW_KEY, JSON.stringify({
                    machine: row.dataset.machine,
                    window: Number(row.dataset.window),
                }));
            } catch (_) {}
        }
    });

    function onReady() {
        ensurePushSubscription();
        syncPushToggleButton();
        if (document.querySelector('.machine-list')) {
            highlightLastWindow();
            syncExpandAllButton();
        }
    }
    if (document.readyState !== 'loading') onReady();
    else document.addEventListener('DOMContentLoaded', onReady);
})();
