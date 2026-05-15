// Wires xterm.js to the per-window WebSocket at /atx/ws and provides the
// mobile helper bar, modifier state machine, swipe-between-windows, and
// iOS install hint.

(function () {
    const view = window.atxView || {};
    if (!view.machine) return;

    const term = new window.Terminal({
        cursorBlink: true,
        fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
        fontSize: 13,
        theme: {
            background: '#0d1117',
            foreground: '#e6edf3',
            cursor: '#e6edf3',
            selectionBackground: '#264f78',
        },
        scrollback: 5000,
        allowProposedApi: true,
    });
    const fit = new window.FitAddon.FitAddon();
    term.loadAddon(fit);
    term.open(document.getElementById('terminal'));
    term.focus();

    // fit-addon needs the container measured. Run on the next frame to be
    // sure flex layout has settled, then once more after a beat in case
    // visualViewport or the helper bar adjusts things.
    function safeFit() {
        try { fit.fit(); } catch (_) {}
    }
    requestAnimationFrame(safeFit);
    setTimeout(safeFit, 50);

    function currentSize() {
        const cols = Math.max(40, term.cols || 80);
        const rows = Math.max(10, term.rows || 24);
        return { cols, rows };
    }
    let resizeTimer = null;
    window.addEventListener('resize', () => {
        fit.fit();
        clearTimeout(resizeTimer);
        resizeTimer = setTimeout(() => {
            const { cols, rows } = currentSize();
            sendJSON({ type: 'resize', cols, rows });
        }, 150);
    });

    const wsURL = (location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/atx/ws';
    const ws = new WebSocket(wsURL);
    ws.binaryType = 'arraybuffer';

    let connected = false;
    let pending = [];

    function sendJSON(obj) {
        const msg = JSON.stringify(obj);
        if (connected) ws.send(msg);
        else pending.push(msg);
    }

    function sendBytes(data) {
        if (!connected) return;
        ws.send(data instanceof Uint8Array ? data : new TextEncoder().encode(data));
    }

    ws.onopen = () => {
        connected = true;
        sendJSON({ type: 'hello' });
        const { cols, rows } = currentSize();
        sendJSON({ type: 'view', machine: view.machine, window: view.window, cols, rows });
        for (const msg of pending) ws.send(msg);
        pending = [];
    };

    ws.onmessage = (e) => {
        if (e.data instanceof ArrayBuffer) {
            term.write(new Uint8Array(e.data));
        } else if (typeof e.data === 'string') {
            try {
                const msg = JSON.parse(e.data);
                if (msg.type === 'error' && msg.error) {
                    term.write('\r\n\x1b[31matx: ' + msg.error + '\x1b[0m\r\n');
                }
            } catch (_) { /* ignore */ }
        }
    };

    ws.onclose = () => {
        connected = false;
        term.write('\r\n\x1b[33matx: connection closed\x1b[0m\r\n');
    };

    // --- modifier state machine + onData interception ---

    const modState = { ctrl: 'idle', alt: 'idle' }; // 'idle' | 'armed' | 'locked'

    function setMod(name, next) {
        modState[name] = next;
        for (const btn of document.querySelectorAll(`.hb-mod[data-mod="${name}"]`)) {
            btn.classList.toggle('armed', next === 'armed');
            btn.classList.toggle('locked', next === 'locked');
        }
    }

    // Apply Ctrl/Alt to the first char of data; subsequent chars pass through
    // unless the modifier is locked (in which case it applies to every char).
    function applyModifiers(data) {
        if (modState.ctrl === 'idle' && modState.alt === 'idle') return data;
        let out = '';
        for (let i = 0; i < data.length; i++) {
            const ch = data[i];
            const code = ch.charCodeAt(0);
            const applyAlt = modState.alt !== 'idle' && (i === 0 || modState.alt === 'locked');
            const applyCtrl = modState.ctrl !== 'idle' && (i === 0 || modState.ctrl === 'locked');
            let outCh = ch;
            if (applyCtrl && code >= 0x40 && code < 0x7f) {
                outCh = String.fromCharCode(code & 0x1f);
            }
            if (applyAlt) outCh = '\x1b' + outCh;
            out += outCh;
        }
        if (modState.ctrl === 'armed') setMod('ctrl', 'idle');
        if (modState.alt === 'armed') setMod('alt', 'idle');
        return out;
    }

    term.onData((data) => {
        sendBytes(applyModifiers(data));
    });

    // --- helper bar ---


    const helperbar = document.getElementById('helperbar');
    // Visibility of the helper bar is entirely CSS-driven now (media
    // queries on (any-pointer: fine) and viewport width); see style.css.

    helperbar.addEventListener('mousedown', (e) => {
        // Don't let the bar steal focus from the terminal.
        if (e.target.closest('.hb-btn')) e.preventDefault();
    });
    helperbar.addEventListener('touchstart', (e) => {
        if (e.target.closest('.hb-btn')) e.preventDefault();
    }, { passive: false });

    // Resolved in JS so raw control bytes never round-trip through HTML
    // attribute parsing.
    const KEY_MAP = {
        esc:   '\x1b',
        tab:   '\t',
        enter: '\r',
        'c-o': '\x0f',
        up:    '\x1b[A',
        down:  '\x1b[B',
        left:  '\x1b[D',
        right: '\x1b[C',
        home:  '\x1bOH',
        end:   '\x1bOF',
        pgup:  '\x1b[5~',
        pgdn:  '\x1b[6~',
    };

    for (const btn of document.querySelectorAll('.hb-btn[data-keyname]')) {
        const seq = KEY_MAP[btn.dataset.keyname];
        btn.addEventListener('click', () => {
            sendBytes(applyModifiers(seq));
            term.focus();
        });
    }
    for (const btn of document.querySelectorAll('.hb-mod')) {
        btn.addEventListener('click', () => {
            const name = btn.dataset.mod;
            const cur = modState[name];
            // Tap cycle: idle → armed → locked → idle.
            const next = cur === 'idle' ? 'armed' : cur === 'armed' ? 'locked' : 'idle';
            setMod(name, next);
            term.focus();
        });
    }

    // --- visualViewport docking: keep the helper bar above the soft keyboard ---

    if (window.visualViewport) {
        const dockBar = () => {
            const vv = window.visualViewport;
            // Distance from layout-viewport bottom to visual-viewport bottom =
            // height of the keyboard (when shown). Move the bar up by that much.
            const liftedPx = Math.max(0, window.innerHeight - vv.height - vv.offsetTop);
            helperbar.style.transform = `translateY(${-liftedPx}px)`;
            // Reserve space below the terminal so output isn't hidden behind it.
            document.body.style.setProperty('--helperbar-lift', `${liftedPx}px`);
            fit.fit();
        };
        window.visualViewport.addEventListener('resize', dockBar);
        window.visualViewport.addEventListener('scroll', dockBar);
        dockBar();
    }

    // --- swipe-between-windows on the terminal area ---

    let touchStartX = 0, touchStartY = 0, touchStartT = 0;
    const SWIPE_MIN_X = 80, SWIPE_MAX_Y = 60, SWIPE_MAX_MS = 500;

    const swipeTarget = document.querySelector('.terminal-host');
    swipeTarget.addEventListener('touchstart', (e) => {
        if (e.touches.length !== 1) return;
        touchStartX = e.touches[0].clientX;
        touchStartY = e.touches[0].clientY;
        touchStartT = Date.now();
    }, { passive: true });
    swipeTarget.addEventListener('touchend', (e) => {
        if (e.changedTouches.length !== 1) return;
        const t = e.changedTouches[0];
        const dx = t.clientX - touchStartX;
        const dy = Math.abs(t.clientY - touchStartY);
        const dt = Date.now() - touchStartT;
        if (Math.abs(dx) < SWIPE_MIN_X || dy > SWIPE_MAX_Y || dt > SWIPE_MAX_MS) return;
        if (dx > 0 && view.prevWindow >= 0) {
            location.href = `/atx/m/${view.machine}/w/${view.prevWindow}`;
        } else if (dx < 0 && view.nextWindow >= 0) {
            location.href = `/atx/m/${view.machine}/w/${view.nextWindow}`;
        }
    }, { passive: true });

    // --- detach on hidden, reattach on visible ---
    // Tearing down the mirror when the tab is hidden releases atx's tmux
    // client so the pane snaps back to the user's mosh-only geometry.

    document.addEventListener('visibilitychange', () => {
        if (document.hidden) {
            sendJSON({ type: 'view_hidden' });
        } else {
            fit.fit();
            const { cols, rows } = currentSize();
            sendJSON({ type: 'view', machine: view.machine, window: view.window, cols, rows });
        }
    });

    // --- iOS install hint (one-time) ---

    const ua = navigator.userAgent;
    const isIOSSafari = /iP(hone|ad|od)/.test(ua) && /Safari/.test(ua) && !/CriOS|FxiOS/.test(ua);
    const isStandalone = window.navigator.standalone === true || matchMedia('(display-mode: standalone)').matches;
    const dismissed = localStorage.getItem('atx.iosInstallDismissed') === '1';
    if (isIOSSafari && !isStandalone && !dismissed) {
        const banner = document.getElementById('ios-install-banner');
        banner.hidden = false;
        document.getElementById('ios-install-dismiss').addEventListener('click', () => {
            banner.hidden = true;
            localStorage.setItem('atx.iosInstallDismissed', '1');
        });
    }
})();
