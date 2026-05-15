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
        // Per-machine window list page (but NOT the terminal page, where
        // wiping <main> would destroy the live xterm.js DOM).
        const m = location.pathname.match(/^\/atx\/m\/([^/]+)\/?$/);
        return !!m && m[1] === machineName;
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

    function onReady() {
        ensurePushSubscription();
        syncPushToggleButton();
    }
    if (document.readyState !== 'loading') onReady();
    else document.addEventListener('DOMContentLoaded', onReady);
})();
