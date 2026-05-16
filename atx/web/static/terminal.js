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
    function refitAndNotify() {
        publishHelperbarHeight();
        safeFit();
        positionCopyCursor();
        clearTimeout(resizeTimer);
        resizeTimer = setTimeout(() => {
            const { cols, rows } = currentSize();
            sendJSON({ type: 'resize', cols, rows });
        }, 150);
    }
    window.addEventListener('resize', refitAndNotify);

    const wsURL = (location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/atx/ws';
    let ws = null;
    let connected = false;
    let pending = [];
    let reconnectTimer = null;
    let reconnectDelay = 500;
    // Set when the tab is returning from hidden (or its WS dropped while
    // hidden) so the next outgoing `view` asks the server to snap to the
    // session's current active window. User-driven views never set this,
    // so picker / arrow / swipe navigation isn't bounced by the snap.
    let wantActiveOnNextView = false;

    function sendJSON(obj) {
        const msg = JSON.stringify(obj);
        if (connected) ws.send(msg);
        else pending.push(msg);
    }

    function sendBytes(data) {
        if (!connected) return;
        ws.send(data instanceof Uint8Array ? data : new TextEncoder().encode(data));
    }

    // Pending WS request → resolver, keyed by reqId. The copy/paste protocol
    // is request/reply: each `copy_*` or `paste_clipboard` message has a
    // generated reqId; the server's reply (copy_state / copied / pasted /
    // error) carries the same reqId and resolves the pending promise.
    const pendingReqs = new Map();
    let reqCounter = 0;
    function wsRequest(type, payload) {
        const reqId = 'r' + (++reqCounter);
        return new Promise((resolve, reject) => {
            const timer = setTimeout(() => {
                if (pendingReqs.has(reqId)) {
                    pendingReqs.delete(reqId);
                    reject(new Error('timeout'));
                }
            }, 10000);
            pendingReqs.set(reqId, {
                resolve: (v) => { clearTimeout(timer); resolve(v); },
                reject: (e) => { clearTimeout(timer); reject(e); },
            });
            sendJSON({ type, reqId, payload });
        });
    }

    // Mobile Safari and PWAs routinely drop the WebSocket when backgrounded.
    // connect() is idempotent and re-attaches the server-side mirror on
    // resume by re-sending `view` in onopen; we never surface the drop to
    // the terminal since the user would just see a misleading error before
    // the reconnect repaint.
    function connect() {
        if (reconnectTimer) { clearTimeout(reconnectTimer); reconnectTimer = null; }
        if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) return;

        // Any queued sends were aimed at the previous session; the fresh
        // hello+view below supersede them.
        pending = [];

        ws = new WebSocket(wsURL);
        ws.binaryType = 'arraybuffer';

        ws.onopen = () => {
            connected = true;
            reconnectDelay = 500;
            sendJSON({ type: 'hello' });
            const { cols, rows } = currentSize();
            sendJSON({ type: 'view', machine: view.machine, window: view.window, cols, rows, wantActive: wantActiveOnNextView });
            wantActiveOnNextView = false;
            for (const msg of pending) ws.send(msg);
            pending = [];
        };

        ws.onmessage = (e) => {
            if (e.data instanceof ArrayBuffer) {
                term.write(new Uint8Array(e.data));
                return;
            }
            if (typeof e.data !== 'string') return;
            let msg;
            try { msg = JSON.parse(e.data); } catch (_) { return; }
            // Reply correlation first; broadcasts (no reqId) fall through.
            if (msg.reqId && pendingReqs.has(msg.reqId)) {
                const p = pendingReqs.get(msg.reqId);
                pendingReqs.delete(msg.reqId);
                if (msg.type === 'error') p.reject(new Error(msg.error || 'error'));
                else p.resolve(msg.payload);
                return;
            }
            if (msg.type === 'error' && msg.error) {
                term.write('\r\n\x1b[31matx: ' + msg.error + '\x1b[0m\r\n');
            } else if (msg.type === 'copy_state' && msg.payload) {
                // Server-initiated push (e.g. on view start after CopyResync).
                applyCopyState(msg.payload);
            } else if (msg.type === 'active_window' && typeof msg.window === 'number') {
                // Native tmux window switch (prefix+n / prefix+l / prefix+0..9
                // in the user's own tmux client). Ignore while hidden — the
                // server pushes a fresh active_window after the next `view`,
                // so we'll snap to the latest on the way back.
                if (document.hidden) return;
                if (msg.machine !== view.machine) return;
                if (msg.window === view.window) return;
                navigateTo(msg.machine, msg.window);
            }
        };

        ws.onclose = () => {
            connected = false;
            for (const p of pendingReqs.values()) p.reject(new Error('disconnected'));
            pendingReqs.clear();
            // While hidden, defer reconnection — no point streaming a mirror
            // the user can't see, and the visibilitychange handler will
            // trigger connect() on resume.
            if (document.hidden) return;
            scheduleReconnect();
        };
    }

    function scheduleReconnect() {
        if (reconnectTimer) return;
        reconnectTimer = setTimeout(() => {
            reconnectTimer = null;
            connect();
        }, reconnectDelay);
        reconnectDelay = Math.min(5000, reconnectDelay * 2);
    }

    connect();

    // --- modifier state machine + onData interception ---

    const modState = { ctrl: 'idle', alt: 'idle' }; // 'idle' | 'armed' | 'locked'

    function setMod(name, next) {
        modState[name] = next;
        for (const btn of document.querySelectorAll(`[data-action="mod:${name}"]`)) {
            btn.classList.toggle('armed', next === 'armed');
            btn.classList.toggle('locked', next === 'locked');
        }
    }

    // Apply armed/locked modifiers to one outbound chunk. Ctrl+arrow /
    // Alt+arrow / Ctrl+PgUp etc. need the modifyOtherKeys CSI encoding
    // (`\x1b[1;5C` for C-Right) — bit-masking the leading 0x1b would just
    // send a bare arrow and the user's `bind -n C-Left/C-Right/M-arrows`
    // would never fire.
    function applyModifiers(data) {
        if (modState.ctrl === 'idle' && modState.alt === 'idle') return data;
        const ctrlOn = modState.ctrl !== 'idle';
        const altOn = modState.alt !== 'idle';
        // xterm modifyOtherKeys param: 1 + (shift?1:0) + (alt?2:0) + (ctrl?4:0).
        const mod = 1 + (altOn ? 2 : 0) + (ctrlOn ? 4 : 0);
        let body, m;
        if ((m = /^\x1b[[O]([ABCDHF])$/.exec(data))) {
            body = `\x1b[1;${mod}${m[1]}`;
        } else if ((m = /^\x1b\[(\d+)~$/.exec(data))) {
            body = `\x1b[${m[1]};${mod}~`;
        } else {
            body = '';
            for (let i = 0; i < data.length; i++) {
                const code = data.charCodeAt(i);
                const ctrlHere = ctrlOn && (i === 0 || modState.ctrl === 'locked');
                const altHere = altOn && (i === 0 || modState.alt === 'locked');
                let ch = data[i];
                if (ctrlHere && code >= 0x40 && code < 0x7f) ch = String.fromCharCode(code & 0x1f);
                if (altHere) ch = '\x1b' + ch;
                body += ch;
            }
        }
        if (modState.ctrl === 'armed') setMod('ctrl', 'idle');
        if (modState.alt === 'armed') setMod('alt', 'idle');
        return body;
    }

    term.onData((data) => {
        sendBytes(applyModifiers(data));
    });

    // --- helper bar ---

    const helperbar = document.getElementById('helperbar');
    // Visibility is CSS-driven: hidden by default, shown in
    // @media (pointer: coarse), (max-width: 600px). See style.css.

    // Measured (not hardcoded as in earlier revisions) so .terminal-host's
    // margin-bottom tracks helper-bar layout and safe-area-inset-bottom
    // exactly — avoids off-by-one clipping of the bottom xterm row on Android.
    let lastHelperbarHeight = -1;
    function publishHelperbarHeight() {
        const h = helperbar.getBoundingClientRect().height;
        if (h > 0 && h !== lastHelperbarHeight) {
            lastHelperbarHeight = h;
            document.body.style.setProperty('--helperbar-height', `${h}px`);
        }
    }
    publishHelperbarHeight();

    // Resolved in JS so raw control bytes never round-trip through HTML
    // attribute parsing.
    const KEY_MAP = {
        esc:   '\x1b',
        tab:   '\t',
        up:    '\x1b[A',
        down:  '\x1b[B',
        left:  '\x1b[D',
        right: '\x1b[C',
        home:  '\x1bOH',
        end:   '\x1bOF',
        pgup:  '\x1b[5~',
        pgdn:  '\x1b[6~',
    };

    function activateHbBtn(btn) {
        const action = btn.dataset.action;
        if (action === 'compose')      { openPromptModal('compose'); return; }
        if (action === 'copy')         { enterCopyMode(); return; }
        if (action === 'paste')        { pasteAction(); return; }
        if (action === 'cmdmenu')      { enterCmdMenu(); return; }
        if (action === 'winnav:prev')  { navigateDelta(-1); return; }
        if (action === 'winnav:next')  { navigateDelta(1); return; }
        if (action.startsWith('copyfn:')) {
            handleCopyFn(action.slice('copyfn:'.length));
            return;
        }
        if (action.startsWith('cmd:')) {
            handleCmdAction(action.slice('cmd:'.length));
            return;
        }
        const sep = action.indexOf(':');
        const kind = action.slice(0, sep);
        const name = action.slice(sep + 1);
        if (kind === 'mod') {
            const cur = modState[name];
            // Tap cycle: idle → armed → locked → idle.
            setMod(name, cur === 'idle' ? 'armed' : cur === 'armed' ? 'locked' : 'idle');
        } else {
            sendBytes(applyModifiers(KEY_MAP[name]));
        }
        term.focus();
    }

    // pointerdown preventDefault keeps the terminal focused (suppresses
    // focus shift to the button + the synthetic mousedown). pointerup,
    // touchend, and click are all bound with a short dedupe window —
    // whichever fires first activates, and the others fall through to
    // the no-op debounce. We bind all three because iOS Safari has been
    // observed suppressing one or another depending on the gesture path.
    let lastFireT = 0;
    function maybeFire(btn) {
        if (!btn) return;
        const now = Date.now();
        if (now - lastFireT < 250) return;
        lastFireT = now;
        activateHbBtn(btn);
    }

    // Arrow-key auto-repeat: holding ←/↑/↓/→ behaves like a physical
    // keyboard — fire once on pointerdown, then after a 400ms initial
    // delay repeat at ~40ms (matches OS keyboard repeat rates).
    const REPEAT_ARROW_KEYS = new Set(['up', 'down', 'left', 'right']);
    const REPEAT_INITIAL_DELAY_MS = 400;
    const REPEAT_INTERVAL_MS = 40;
    let holdInitialT = 0;
    let holdIntervalT = 0;
    let inHoldGesture = false;
    function isRepeatArrowBtn(btn) {
        if (!btn) return false;
        const a = btn.dataset.action || '';
        return a.startsWith('key:') && REPEAT_ARROW_KEYS.has(a.slice(4));
    }
    function stopHoldRepeat() {
        if (holdInitialT) { clearTimeout(holdInitialT); holdInitialT = 0; }
        if (holdIntervalT) { clearInterval(holdIntervalT); holdIntervalT = 0; }
    }
    function endHoldGesture() {
        inHoldGesture = false;
        stopHoldRepeat();
    }

    helperbar.addEventListener('pointerdown', (e) => {
        const btn = e.target.closest('.hb-btn');
        if (!btn) return;
        e.preventDefault();
        if (isRepeatArrowBtn(btn)) {
            // Fire the first arrow immediately, then arm the repeat. The
            // synthetic pointerup/touchend/click that follow this gesture
            // are swallowed by inHoldGesture so they don't double-fire.
            stopHoldRepeat();
            inHoldGesture = true;
            lastFireT = Date.now();
            activateHbBtn(btn);
            holdInitialT = setTimeout(() => {
                holdInitialT = 0;
                holdIntervalT = setInterval(() => {
                    lastFireT = Date.now();
                    activateHbBtn(btn);
                }, REPEAT_INTERVAL_MS);
            }, REPEAT_INITIAL_DELAY_MS);
        }
    });
    helperbar.addEventListener('pointerup', (e) => {
        if (inHoldGesture) { endHoldGesture(); return; }
        maybeFire(e.target.closest('.hb-btn'));
    });
    helperbar.addEventListener('pointercancel', () => {
        if (inHoldGesture) endHoldGesture();
    });
    helperbar.addEventListener('touchend', (e) => {
        if (inHoldGesture) { endHoldGesture(); return; }
        maybeFire(e.target.closest('.hb-btn'));
    });
    helperbar.addEventListener('click', (e) => {
        if (inHoldGesture) return;
        maybeFire(e.target.closest('.hb-btn'));
    });

    // --- copy mode ---
    //
    // Driven by tmux server-side: tap 📋, the server runs `tmux copy-mode`
    // and reports the cursor (`copy_cursor_x/y` via display-message). The
    // helper bar morphs (data-mode="copy") to motion/mark/yank controls.
    // Touch on .terminal-host becomes tap-to-position / drag-to-extend.
    //
    // Cursor is tracked by the server: every gesture's response includes
    // the post-action cursor, which we cache here and pass back as the
    // next gesture's `from` (server computes deltas). We never dead-reckon.

    let copyState = null;        // { inMode, row, col, width, height }
    let marking = false;         // whether `begin-selection` is active
    let dragPhase = 'none';      // 'none' | 'initializing' | 'extending'
    let touchStartCell = null;   // {row, col} at touchstart, in copy mode
    let pendingMoveTarget = null;
    let moveInFlight = false;
    // Cached cell geometry for the lifetime of one drag gesture. Without
    // this, every touchmove + every reply re-runs getBoundingClientRect,
    // forcing layout 3× per round-trip at 60Hz.
    let dragGeom = null;         // { xtRect, hostRect, cellW, cellH }
    const DRAG_SLOP_PX = 8;

    // Swipe-scroll: drag-to-scroll the tmux buffer. Internally drives
    // copy-mode (the only way tmux exposes scrollback) while suppressing
    // the helperbar morph + cursor overlay so it feels like a plain
    // scrollview. We track net upward rows so a swipe-down past the live
    // tail can auto-`cancel` and return to the normal pane.
    let scrollMode = false;
    let scrollCellH = 0;
    let scrollLastY = 0;
    let pendingScrollRows = 0;   // signed; +n queues scroll-down×n, -n queues scroll-up×n
    let scrollPumpInFlight = false;
    let scrollEntering = false;
    let scrollNetRows = 0;       // rows of upward scroll since we entered scrollMode
    let scrollExitAfterDrain = false;

    function emptyCopyState() {
        return { inMode: false, row: 0, col: 0, width: 0, height: 0 };
    }

    function cellGeom() {
        const xtRect = term.element.getBoundingClientRect();
        const hostRect = swipeTarget.getBoundingClientRect();
        return {
            xtRect, hostRect,
            cellW: xtRect.width / Math.max(1, term.cols),
            cellH: xtRect.height / Math.max(1, term.rows),
        };
    }

    const copyButton = document.querySelector('.hb-copy');
    const markButton = document.querySelector('.hb-copy-mark');
    const copyCursorEl = document.getElementById('copy-cursor');
    const copyToastEl = document.getElementById('copy-toast');
    const pasteCatcherEl = document.getElementById('paste-catcher');
    const pasteCatcherTarget = document.getElementById('paste-catcher-target');
    const pasteCatcherCancel = document.getElementById('paste-catcher-cancel');

    function applyCopyState(state) {
        const next = state || emptyCopyState();
        // No-op early-return: during a drag the server replies at ~10Hz
        // and most replies land in the same cell. Setting attributes /
        // re-styling the cursor div every time forces a layout per reply.
        if (copyState &&
            copyState.inMode === next.inMode &&
            copyState.row === next.row &&
            copyState.col === next.col &&
            copyState.width === next.width &&
            copyState.height === next.height) {
            return;
        }
        const wasInMode = copyState && copyState.inMode;
        copyState = next;
        if (copyState.inMode) {
            // scrollMode keeps copy-mode invisible: no helperbar morph, no
            // copy-button active state, no cursor overlay (handled below).
            if (!wasInMode && !scrollMode) helperbar.setAttribute('data-mode', 'copy');
            if (copyButton && !scrollMode) copyButton.classList.add('is-active');
        } else {
            helperbar.removeAttribute('data-mode');
            if (copyButton) copyButton.classList.remove('is-active');
            if (markButton) markButton.classList.remove('is-active');
            marking = false;
            dragPhase = 'none';
        }
        positionCopyCursor();
    }

    function positionCopyCursor() {
        if (!copyState || !copyState.inMode || scrollMode) {
            copyCursorEl.hidden = true;
            return;
        }
        const g = dragGeom || cellGeom();
        copyCursorEl.style.left = (g.xtRect.left - g.hostRect.left + copyState.col * g.cellW) + 'px';
        copyCursorEl.style.top = (g.xtRect.top - g.hostRect.top + copyState.row * g.cellH) + 'px';
        copyCursorEl.style.width = g.cellW + 'px';
        copyCursorEl.style.height = g.cellH + 'px';
        copyCursorEl.hidden = false;
    }

    function touchToCell(t) {
        const g = dragGeom || cellGeom();
        const col = Math.max(0, Math.min(term.cols - 1, Math.floor((t.clientX - g.xtRect.left) / g.cellW)));
        const row = Math.max(0, Math.min(term.rows - 1, Math.floor((t.clientY - g.xtRect.top) / g.cellH)));
        return { row, col };
    }

    function enterCopyMode() {
        // Explicit user action — override any silent swipe-scroll session so
        // the helperbar morphs and the copy cursor becomes visible.
        scrollMode = false;
        scrollExitAfterDrain = false;
        wsRequest('copy_enter', {}).then((st) => {
            marking = false;
            if (markButton) markButton.classList.remove('is-active');
            applyCopyState(st);
        }).catch((e) => console.warn('atx: enter copy:', e));
    }

    // Single-flight copy_move: one in-flight at a time, with the most recent
    // target queued. After each reply the next move uses the freshly-updated
    // server cursor as `from`, so deltas stay correct even if the user is
    // dragging faster than the SSH round-trip.
    function scheduleMove(target) {
        pendingMoveTarget = target;
        if (moveInFlight) return;
        pumpMove();
    }
    function pumpMove() {
        if (!pendingMoveTarget || !copyState || !copyState.inMode) return;
        const target = pendingMoveTarget;
        pendingMoveTarget = null;
        moveInFlight = true;
        wsRequest('copy_move', {
            fromRow: copyState.row, fromCol: copyState.col,
            toRow: target.row, toCol: target.col,
        }).then(applyCopyState).catch((e) => {
            console.warn('atx: copy_move:', e);
        }).finally(() => {
            moveInFlight = false;
            pumpMove();
        });
    }

    // Swipe-scroll: lazily put the pane in copy-mode so subsequent
    // scroll-up / scroll-down send-keys actually have something to drive.
    // Called once per swipe gesture, when the touch first clears the
    // vertical slop. Pending rows queued before this resolves drain via
    // pumpScroll's finally.
    function scrollEnter() {
        if (scrollEntering || scrollMode) return;
        if (copyState && copyState.inMode) {
            // Already in copy-mode (user pressed 📋 first). Don't take over.
            return;
        }
        scrollEntering = true;
        scrollMode = true;
        scrollNetRows = 0;
        wsRequest('copy_enter', {}).then(applyCopyState).catch((e) => {
            console.warn('atx: scroll enter:', e);
            scrollMode = false;
            pendingScrollRows = 0;
        }).finally(() => {
            scrollEntering = false;
            pumpScroll();
        });
    }

    // Coalescing pump for scroll-up / scroll-down. Accumulated rows from
    // touchmove get drained as one `send-keys -X -N count` per round-trip,
    // so a fast drag is bounded by the SSH RTT, not the touchmove rate.
    function pumpScroll() {
        if (scrollPumpInFlight || scrollEntering) return;
        if (!copyState || !copyState.inMode) return;
        if (pendingScrollRows === 0) {
            if (scrollExitAfterDrain) {
                scrollExitAfterDrain = false;
                // Decide AFTER drain: the last queued scroll-down may have
                // brought us back to (or past) the live tail; only then is
                // it right to drop the user out of copy-mode.
                if (scrollNetRows <= 0) doScrollExit();
            }
            return;
        }
        const n = pendingScrollRows;
        pendingScrollRows = 0;
        scrollPumpInFlight = true;
        const name = n < 0 ? 'scroll-up' : 'scroll-down';
        const count = Math.abs(n);
        // Net upward rows: scroll-up adds, scroll-down subtracts (and tmux
        // clamps at the live tail, so netRows going ≤ 0 means we're back).
        scrollNetRows += (n < 0 ? count : -count);
        wsRequest('copy_action', { name, count }).then(applyCopyState).catch((e) => {
            console.warn('atx: scroll:', e);
        }).finally(() => {
            scrollPumpInFlight = false;
            pumpScroll();
        });
    }

    // touchend handler entry point: drain any queued scroll actions, then
    // if we've netted back to the live tail (or past it), cancel out of
    // copy-mode. Otherwise stay in scrollMode silently so the next swipe
    // can resume from the same offset.
    function scrollEndDrag() {
        if (!scrollMode) return;
        // Defer the netRows check to pumpScroll's drain-complete branch:
        // pending scroll-downs may still flip us back to the live tail.
        scrollExitAfterDrain = true;
        pumpScroll();
    }

    function doScrollExit() {
        scrollMode = false;
        scrollNetRows = 0;
        if (!copyState || !copyState.inMode) return;
        wsRequest('copy_action', { name: 'cancel', count: 1 })
            .then(applyCopyState)
            .catch((e) => console.warn('atx: scroll exit:', e));
    }

    async function initDrag(start) {
        dragPhase = 'initializing';
        try {
            let st = await wsRequest('copy_move', {
                fromRow: copyState.row, fromCol: copyState.col,
                toRow: start.row, toCol: start.col,
            });
            applyCopyState(st);
            st = await wsRequest('copy_action', { name: 'begin-selection', count: 1 });
            applyCopyState(st);
            marking = true;
            if (markButton) markButton.classList.add('is-active');
            dragPhase = 'extending';
            // touchmove may have stored a later target; flush it.
            if (pendingMoveTarget) pumpMove();
        } catch (e) {
            console.warn('atx: drag init:', e);
            dragPhase = 'none';
        }
    }

    function handleCopyFn(name) {
        if (!copyState || !copyState.inMode) return;
        if (name === 'yank') { yankAction(); return; }
        if (name === 'done' || name === 'cancel') {
            wsRequest('copy_cancel', {}).then((st) => {
                marking = false;
                if (markButton) markButton.classList.remove('is-active');
                applyCopyState(st);
            }).catch((e) => console.warn('atx: cancel:', e));
            return;
        }
        if (name === 'mark') {
            const act = marking ? 'clear-selection' : 'begin-selection';
            marking = !marking;
            if (markButton) markButton.classList.toggle('is-active', marking);
            wsRequest('copy_action', { name: act, count: 1 })
                .then(applyCopyState).catch((e) => console.warn('atx: mark:', e));
            return;
        }
        const motion = name === 'word-left' ? 'previous-word'
                     : name === 'word-right' ? 'next-word'
                     : name;
        wsRequest('copy_action', { name: motion, count: 1 })
            .then(applyCopyState).catch((e) => console.warn('atx:', name, e));
    }

    // Yank — synchronously initiate the OS-clipboard write inside the user
    // gesture (here, the tap that triggered activateHbBtn). Use a
    // Promise-valued ClipboardItem so Safari extends transient activation
    // until the WS round-trip completes. On rejection, fall back to a toast
    // the user can tap to copy inside a fresh gesture.
    function yankAction() {
        let resolveText;
        const textPromise = new Promise((r) => { resolveText = r; });
        wsRequest('copy_yank', {}).then((p) => {
            const text = (p && p.text) || '';
            resolveText(text);
            // Yank-and-cancel exited copy mode server-side.
            applyCopyState(emptyCopyState());
        }).catch((e) => {
            console.warn('atx: yank:', e);
            resolveText('');
        });

        if (window.ClipboardItem && navigator.clipboard && navigator.clipboard.write) {
            const blobPromise = textPromise.then((t) => new Blob([t], { type: 'text/plain' }));
            navigator.clipboard.write([new ClipboardItem({ 'text/plain': blobPromise })])
                .catch(() => textPromise.then(showCopyToast).catch(() => {}));
            return;
        }
        // Older browsers: writeText after the round-trip; may fail without a
        // gesture, in which case the toast offers a retry.
        textPromise.then((text) => {
            if (!text || !navigator.clipboard) return;
            navigator.clipboard.writeText(text).catch(() => showCopyToast(text));
        });
    }

    let toastTimer = null;
    let toastOnTap = null;
    function showCopyToast(text) {
        if (!text) return;
        if (toastTimer) clearTimeout(toastTimer);
        if (toastOnTap) copyToastEl.removeEventListener('click', toastOnTap);
        copyToastEl.hidden = false;
        toastOnTap = () => {
            copyToastEl.hidden = true;
            copyToastEl.removeEventListener('click', toastOnTap);
            toastOnTap = null;
            if (navigator.clipboard && navigator.clipboard.writeText) {
                navigator.clipboard.writeText(text).catch(() => {});
            }
        };
        copyToastEl.addEventListener('click', toastOnTap);
        toastTimer = setTimeout(() => {
            copyToastEl.hidden = true;
            if (toastOnTap) copyToastEl.removeEventListener('click', toastOnTap);
            toastOnTap = null;
            toastTimer = null;
        }, 8000);
    }

    // Paste — try clipboard.readText (works on desktop / Android). iOS
    // Safari gates it behind a system Paste callout, so on rejection we
    // open a contenteditable modal and capture the `paste` ClipboardEvent.
    async function pasteAction() {
        let text = null;
        try {
            if (navigator.clipboard && navigator.clipboard.readText) {
                text = await navigator.clipboard.readText();
            } else {
                throw new Error('no readText');
            }
        } catch (_) {
            text = await openPasteCatcher().catch(() => null);
        }
        if (text == null || text === '') return;
        wsRequest('paste_clipboard', { text }).catch((e) => console.warn('atx: paste:', e));
    }

    function openPasteCatcher() {
        return new Promise((resolve, reject) => {
            pasteCatcherEl.hidden = false;
            pasteCatcherTarget.textContent = '';
            requestAnimationFrame(() => pasteCatcherTarget.focus());
            let done = false;
            const cleanup = () => {
                pasteCatcherEl.hidden = true;
                pasteCatcherTarget.textContent = '';
                pasteCatcherTarget.removeEventListener('paste', onPaste);
                pasteCatcherCancel.removeEventListener('click', onCancel);
                term.focus();
            };
            const onPaste = (e) => {
                if (done) return;
                done = true;
                e.preventDefault();
                const text = e.clipboardData ? e.clipboardData.getData('text/plain') : '';
                cleanup();
                resolve(text);
            };
            const onCancel = () => {
                if (done) return;
                done = true;
                cleanup();
                reject(new Error('cancelled'));
            };
            pasteCatcherTarget.addEventListener('paste', onPaste);
            pasteCatcherCancel.addEventListener('click', onCancel);
        });
    }

    // --- compose-prompt modal (dual-purpose: free-text compose, or rename) ---

    const promptModal = document.getElementById('prompt-modal');
    const promptTextarea = document.getElementById('prompt-modal-textarea');
    const promptClose = document.getElementById('prompt-modal-close');
    const promptSubmit = document.getElementById('prompt-modal-submit');
    // Tracks how submit should behave: 'compose' sends text into the pane;
    // 'rename' calls tmux_cmd rename. Set when opening and read on submit.
    let promptMode = 'compose';

    function openPromptModal(mode) {
        promptMode = mode || 'compose';
        if (promptMode === 'rename') {
            // Prefill with the current window's name and select it all so a
            // tap-and-type immediately overwrites, while preserving the
            // edit-in-place option.
            const w = windowOf(view.machine, view.window);
            const prefill = (w && w.name) || '';
            promptTextarea.value = prefill;
            promptTextarea.placeholder = 'Window name';
            promptSubmit.disabled = prefill.length === 0;
        } else {
            promptTextarea.value = '';
            promptTextarea.placeholder = 'Compose…';
            promptSubmit.disabled = true;
        }
        promptModal.hidden = false;
        // Defer focus to next frame so the keyboard pops up reliably on iOS.
        requestAnimationFrame(() => {
            promptTextarea.focus();
            if (promptMode === 'rename' && promptTextarea.value.length > 0) {
                promptTextarea.select();
            }
            dockBar();
        });
    }
    function closePromptModal() {
        promptModal.hidden = true;
        promptTextarea.value = '';
        promptSubmit.disabled = true;
        promptMode = 'compose';
        term.focus();
        dockBar();
    }
    function tryCloseWithConfirm() {
        // Don't pester for confirmation on a rename: the field starts non-empty
        // by design (prefilled), so the "you'll lose your text" prompt is noise.
        if (promptMode !== 'rename' && promptTextarea.value.length > 0 && !confirm('Discard this prompt?')) return;
        closePromptModal();
    }

    promptTextarea.addEventListener('input', () => {
        promptSubmit.disabled = promptTextarea.value.length === 0;
    });
    promptClose.addEventListener('click', tryCloseWithConfirm);
    promptSubmit.addEventListener('click', () => {
        const text = promptTextarea.value;
        if (text.length === 0) return;
        if (promptMode === 'rename') {
            runTmuxCmd({ action: 'rename', name: text }).catch(() => {});
            closePromptModal();
            return;
        }
        sendBytes(text);
        closePromptModal();
    });

    // --- command menu (inline helperbar swap) ---

    const closeConfirmModal = document.getElementById('close-confirm-modal');
    const closeConfirmTitle = document.getElementById('close-confirm-title');
    const closeConfirmYes = document.getElementById('close-confirm-yes');
    const closeConfirmCancel = document.getElementById('close-confirm-cancel');

    function enterCmdMenu() {
        helperbar.setAttribute('data-mode', 'cmd');
        // Helper-bar height changes when the menu swaps to two rows; refit so
        // the terminal margin tracks it and rows aren't clipped.
        refitAndNotify();
    }
    function dismissCmdMenu() {
        if (helperbar.getAttribute('data-mode') === 'cmd') {
            helperbar.removeAttribute('data-mode');
            refitAndNotify();
        }
    }

    // Single point of contact for the WS tmux_cmd reply: apply the
    // server-reported active window if it changed, and refresh the cached
    // machines list so the picker/window-name/title catch up to the new
    // tmux state (rename, new, close, renumber, swap all mutate windows).
    function applyCmdResult(res) {
        if (res && typeof res.activeWindow === 'number' && res.activeWindow !== view.window) {
            navigateTo(view.machine, res.activeWindow);
        }
        refreshMachines();
    }
    async function runTmuxCmd(payload) {
        try {
            const res = await wsRequest('tmux_cmd', payload);
            applyCmdResult(res);
            return res;
        } catch (e) {
            console.warn('atx: tmux_cmd', payload, e);
            throw e;
        }
    }

    function handleCmdAction(name) {
        if (name === 'dismiss') { dismissCmdMenu(); return; }
        if (name === 'new') {
            runTmuxCmd({ action: 'new' }).catch(() => {}).finally(dismissCmdMenu);
            return;
        }
        if (name === 'rename') {
            // Compose modal takes over from here; it handles its own dismiss
            // and the helperbar reverts when we leave cmd mode.
            dismissCmdMenu();
            openPromptModal('rename');
            return;
        }
        if (name === 'close') {
            const w = windowOf(view.machine, view.window);
            const label = w && w.name ? `${view.window} "${w.name}"` : `${view.window}`;
            closeConfirmTitle.textContent = `Close window ${label}?`;
            closeConfirmOpenedAt = Date.now();
            closeConfirmModal.hidden = false;
            return;
        }
        if (name === 'swap-prev' || name === 'swap-next') {
            // ◀ / ▶ keep the menu open for repeat taps; user dismisses with ✕.
            runTmuxCmd({ action: name }).catch(() => {});
            return;
        }
        if (name === 'renumber') {
            runTmuxCmd({ action: 'renumber' }).catch(() => {}).finally(dismissCmdMenu);
            return;
        }
    }

    // Timestamp of the most recent reveal so the backdrop handler can
    // ignore the synthetic click from the helperbar tap that opened it —
    // browsers that retarget click events after DOM mutation (Chrome on
    // Android most reliably) would otherwise dispatch on the backdrop now
    // under the user's finger and dismiss before the modal is even seen.
    let closeConfirmOpenedAt = 0;
    function bindCloseConfirm() {
        const fire = (yes) => {
            closeConfirmModal.hidden = true;
            // Mirror of the open-side guard at closeConfirmOpenedAt: once the
            // modal is hidden, the synthetic click that follows pointerup
            // retargets to whatever's now under the finger — typically the
            // helperbar's cmd:close button that opened this modal in the
            // first place — which would immediately re-open it. Bumping
            // lastFireT lets the helperbar's own dedupe swallow that click.
            lastFireT = Date.now();
            if (!yes) { dismissCmdMenu(); return; }
            runTmuxCmd({ action: 'close' }).catch(() => {}).finally(dismissCmdMenu);
        };
        // Mirror the helperbar's gesture handling: pointerdown.preventDefault
        // suppresses focus shift + synthetic mousedown, then pointerup,
        // touchend, and click are all bound on the modal parent with a 250ms
        // dedupe — whichever event fires first wins, the others no-op. iOS
        // Safari suppresses one or another depending on gesture path, and
        // per-button listeners haven't been reliable here.
        let lastT = 0;
        const handler = (e) => {
            const now = Date.now();
            if (now - lastT < 250) return;
            const btn = e.target.closest('button');
            if (btn === closeConfirmYes)         { lastT = now; fire(true); return; }
            if (btn === closeConfirmCancel)      { lastT = now; fire(false); return; }
            if (e.target === closeConfirmModal && now - closeConfirmOpenedAt >= 400) {
                lastT = now;
                fire(false);
            }
        };
        closeConfirmModal.addEventListener('pointerdown', (e) => {
            if (e.target.closest('button')) e.preventDefault();
        });
        closeConfirmModal.addEventListener('pointerup', handler);
        closeConfirmModal.addEventListener('touchend', handler);
        closeConfirmModal.addEventListener('click', handler);
    }
    bindCloseConfirm();

    // --- visualViewport docking: keep the helper bar (or the compose
    // modal's send button, when that modal is open) above the soft keyboard ---

    let lastFittedLift = -1;
    function dockBar() {
        if (!window.visualViewport) return;
        const vv = window.visualViewport;
        // Distance from layout-viewport bottom to visual-viewport bottom =
        // height of the keyboard (when shown).
        const liftedPx = Math.max(0, window.innerHeight - vv.height - vv.offsetTop);
        document.body.style.setProperty('--helperbar-lift', `${liftedPx}px`);
        if (promptModal.hidden) {
            helperbar.style.transform = `translateY(${-liftedPx}px)`;
            promptSubmit.style.transform = '';
            // visualViewport.scroll fires per-frame on iOS; skip the refit
            // when nothing actually moved. --helperbar-lift shrinks
            // .terminal-host's margin-bottom so the terminal sits above the
            // lifted helper bar; refit propagates the new row count to tmux.
            if (liftedPx !== lastFittedLift) {
                lastFittedLift = liftedPx;
                refitAndNotify();
            }
        } else {
            // Modal covers the terminal, so don't refit; just lift the send
            // button above the keyboard so the user can tap it without
            // dismissing the keyboard first.
            promptSubmit.style.transform = `translateY(${-liftedPx}px)`;
        }
    }
    if (window.visualViewport) {
        window.visualViewport.addEventListener('resize', dockBar);
        window.visualViewport.addEventListener('scroll', dockBar);
        dockBar();
    }

    // --- header navigation: arrows, machine picker, window picker ---

    const MACHINE_LAST_KEY = 'atx.machineLastWindow';
    const GLOBAL_LAST_KEY = 'atx.lastWindow';

    function readMachineLast() {
        try {
            const obj = JSON.parse(localStorage.getItem(MACHINE_LAST_KEY) || '{}');
            return obj && typeof obj === 'object' ? obj : {};
        } catch (_) { return {}; }
    }
    // recordLast updates both the per-machine map (for picker fallback
    // when switching machines) and atx.lastWindow (for the unified
    // view's last-used highlight on app.js side) — app.js's click
    // handler only writes atx.lastWindow on .window-row clicks, so
    // arrival via picker/arrow/swipe/deep-link/push needs to mirror it.
    function recordLast(machine, idx) {
        const obj = readMachineLast();
        obj[machine] = idx;
        try { localStorage.setItem(MACHINE_LAST_KEY, JSON.stringify(obj)); } catch (_) {}
        try { localStorage.setItem(GLOBAL_LAST_KEY, JSON.stringify({ machine, window: idx })); } catch (_) {}
    }
    recordLast(view.machine, view.window);

    if (!Array.isArray(view.machines)) view.machines = [];

    function escapeHTML(s) {
        return String(s).replace(/[&<>"']/g, (c) => ({
            '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
        }[c]));
    }
    function machineByName(name) {
        return view.machines.find((m) => m.name === name);
    }
    function windowOf(machineName, idx) {
        const m = machineByName(machineName);
        return m && m.windows ? m.windows.find((w) => w.index === idx) : null;
    }

    const stripeEl = document.querySelector('.terminal-stripe');
    const machineSizerEl = document.querySelector('.terminal-machine-sizer');
    const machineLabelEl = document.querySelector('.terminal-machine');
    const windowLabelEl = document.querySelector('.terminal-window-name');

    function syncMachineGhosts() {
        // Rebuild the hidden ghost spans so the segment width tracks the
        // widest currently-known machine name even as the list refreshes.
        for (const g of machineSizerEl.querySelectorAll('.terminal-machine-ghost')) g.remove();
        for (const m of view.machines || []) {
            const g = document.createElement('span');
            g.className = 'terminal-machine-ghost';
            g.setAttribute('aria-hidden', 'true');
            g.textContent = m.display || m.name;
            machineSizerEl.appendChild(g);
        }
    }

    function updateHeader() {
        const m = machineByName(view.machine);
        const w = windowOf(view.machine, view.window);
        const display = m?.display ?? view.machine;
        const name = w?.name ?? `w${view.window}`;
        if (m?.color) stripeEl.style.background = m.color;
        machineLabelEl.textContent = display;
        windowLabelEl.textContent = name;
        document.title = `atx — ${display} · ${view.window}${w?.name ? ' ' + w.name : ''}`;
    }

    // navigateTo is the public entry point; popstate calls applyView
    // directly so the popped URL isn't re-pushed.
    function applyView(machine, idx) {
        if (machine === view.machine && idx === view.window) return;
        view.machine = machine;
        view.window = idx;
        recordLast(machine, idx);
        updateHeader();
        for (const p of pickers) p.rerenderIfOpen();
        // reset() rather than clear() so prior window's scrollback and
        // parser state don't leak into the new mirror's repaint.
        term.reset();
        const { cols, rows } = currentSize();
        sendJSON({ type: 'view', machine, window: idx, cols, rows });
    }
    function navigateTo(machine, idx) {
        if (machine === view.machine && idx === view.window) return;
        const url = `/atx/m/${encodeURIComponent(machine)}/w/${idx}`;
        if (location.pathname !== url) history.pushState({}, '', url);
        applyView(machine, idx);
    }
    window.addEventListener('popstate', () => {
        const m = location.pathname.match(/^\/atx\/m\/([^\/]+)\/w\/(\d+)/);
        if (!m) return;
        applyView(decodeURIComponent(m[1]), Number(m[2]));
    });

    function navigateDelta(delta) {
        const m = machineByName(view.machine);
        if (!m || !m.windows || m.windows.length === 0) return;
        const i = m.windows.findIndex((w) => w.index === view.window);
        if (i < 0) return;
        const n = m.windows.length;
        const next = m.windows[((i + delta) % n + n) % n];
        if (!next || next.index === view.window) return;
        navigateTo(view.machine, next.index);
    }
    // --- pickers ---

    const pickers = [];
    function makePicker(triggerId, popoverId, render, onSelect) {
        const trigger = document.getElementById(triggerId);
        const popover = document.getElementById(popoverId);
        const p = {
            trigger, popover,
            get open() { return !popover.hidden; },
            set(state) {
                popover.hidden = !state;
                trigger.setAttribute('aria-expanded', state ? 'true' : 'false');
            },
            rerenderIfOpen() { if (!popover.hidden) render(popover); },
        };
        trigger.addEventListener('click', (e) => {
            e.stopPropagation();
            if (p.open) { p.set(false); return; }
            for (const o of pickers) if (o !== p) o.set(false);
            render(popover);
            p.set(true);
            refreshMachines();
        });
        popover.addEventListener('click', (e) => {
            const row = e.target.closest('.picker-row');
            if (!row || row.classList.contains('is-disabled')) return;
            p.set(false);
            onSelect(row);
        });
        pickers.push(p);
        return p;
    }

    function renderMachinePopover(popover) {
        if (view.machines.length === 0) {
            popover.innerHTML = '<div class="picker-empty">No machines.</div>';
            return;
        }
        const parts = [];
        for (const m of view.machines) {
            const isCurrent = m.name === view.machine;
            const disabled = !m.online;
            const count = m.windowCount || 0;
            const countLabel = count + ' window' + (count === 1 ? '' : 's');
            const cls = [
                'picker-row',
                isCurrent ? 'is-current' : '',
                disabled ? 'is-disabled' : '',
            ].filter(Boolean).join(' ');
            parts.push(
                `<div class="${cls}" data-machine="${escapeHTML(m.name)}" role="menuitem"${disabled ? ' aria-disabled="true"' : ''}>` +
                    `<span class="picker-dot ${isCurrent ? 'picker-dot-current' : ''}" aria-hidden="true"></span>` +
                    `<span class="picker-stripe" style="background:${m.color}" aria-hidden="true"></span>` +
                    `<span class="picker-name">${escapeHTML(m.display || m.name)}</span>` +
                    `<span class="picker-count">${countLabel}</span>` +
                `</div>`
            );
        }
        popover.innerHTML = parts.join('');
    }

    function renderWindowPopover(popover) {
        const m = machineByName(view.machine);
        if (!m || !m.windows || m.windows.length === 0) {
            popover.innerHTML = '<div class="picker-empty">No windows.</div>';
            return;
        }
        const parts = [];
        for (const w of m.windows) {
            const isCurrent = w.index === view.window;
            const cls = ['picker-row', isCurrent ? 'is-current' : ''].filter(Boolean).join(' ');
            parts.push(
                `<div class="${cls}" data-window="${w.index}" role="menuitem">` +
                    `<span class="picker-dot ${isCurrent ? 'picker-dot-current' : ''}" aria-hidden="true"></span>` +
                    `<span class="picker-index">${w.index}</span>` +
                    `<span class="picker-name">${escapeHTML(w.name || '')}</span>` +
                `</div>`
            );
        }
        popover.innerHTML = parts.join('');
    }

    makePicker('terminal-picker-machine', 'terminal-picker-machine-popover',
        renderMachinePopover, (row) => {
            const name = row.dataset.machine;
            const m = machineByName(name);
            if (!m) return;
            if (!m.windows || m.windows.length === 0) {
                // Machine is online but has no tmux windows yet — there's
                // nothing to attach to, so fall back to the unified view.
                location.href = `/atx/m/${encodeURIComponent(name)}`;
                return;
            }
            const last = readMachineLast()[name];
            const target = m.windows.find((w) => w.index === last) || m.windows[0];
            navigateTo(name, target.index);
        });

    makePicker('terminal-picker-window', 'terminal-picker-window-popover',
        renderWindowPopover, (row) => {
            navigateTo(view.machine, Number(row.dataset.window));
        });

    let refreshInFlight = null;
    async function refreshMachines() {
        if (refreshInFlight) return refreshInFlight;
        refreshInFlight = (async () => {
            try {
                const r = await fetch('/atx/api/machines', { cache: 'no-store' });
                if (!r.ok) return;
                const data = await r.json();
                if (!data || !Array.isArray(data.machines)) return;
                view.machines = data.machines;
                syncMachineGhosts();
                updateHeader();
                for (const p of pickers) p.rerenderIfOpen();
            } catch (_) { /* keep stale data */ }
        })();
        try { await refreshInFlight; } finally { refreshInFlight = null; }
    }

    document.addEventListener('click', (e) => {
        if (!pickers.some((p) => p.open)) return;
        for (const p of pickers) {
            if (!p.open) continue;
            if (p.popover.contains(e.target) || p.trigger.contains(e.target)) continue;
            p.set(false);
        }
    });

    // --- touch on the terminal area ---
    // Out of copy mode: vertical drag scrolls tmux's real scrollback by
    // driving copy-mode under the hood (the only way tmux exposes it).
    // scrollMode keeps the helperbar in its normal layout — see
    // applyCopyState. On lift, if we've netted back to the live tail, we
    // `cancel` so the user never has to think about copy-mode.
    // In user-initiated copy mode (📋 button): tap-to-position, drag-to-
    // extend selection, as before.

    let touchStartX = 0, touchStartY = 0;

    const swipeTarget = document.querySelector('.terminal-host');
    swipeTarget.addEventListener('touchstart', (e) => {
        if (e.touches.length !== 1) return;
        touchStartX = e.touches[0].clientX;
        touchStartY = e.touches[0].clientY;
        if (copyState && copyState.inMode && !scrollMode) {
            // User-initiated copy-mode: drag-to-extend selection.
            dragGeom = cellGeom();
            touchStartCell = touchToCell(e.touches[0]);
            dragPhase = 'none';
            pendingMoveTarget = null;
            return;
        }
        // Scroll-drag path. Defer copy_enter until we know it's vertical.
        scrollCellH = term.element.getBoundingClientRect().height / Math.max(1, term.rows);
        scrollLastY = touchStartY;
    }, { passive: true });
    swipeTarget.addEventListener('touchmove', (e) => {
        if (e.touches.length !== 1) return;
        const t = e.touches[0];
        if (copyState && copyState.inMode && !scrollMode) {
            // Select-drag (unchanged behavior from user-initiated copy mode).
            const dist = Math.hypot(t.clientX - touchStartX, t.clientY - touchStartY);
            if (dist < DRAG_SLOP_PX) return;
            const cell = touchToCell(t);
            if (dragPhase === 'none') {
                // First crossing of slop → auto-Mark: move cursor to start
                // cell, begin selection, then start extending to the current
                // touch location. initDrag sequences these awaits so the
                // selection always anchors at touchStartCell, never wherever
                // the cursor happened to be pre-drag.
                pendingMoveTarget = cell;
                initDrag(touchStartCell);
            } else if (dragPhase === 'extending') {
                scheduleMove(cell);
            } else {
                // 'initializing' — store latest target; flush after init.
                pendingMoveTarget = cell;
            }
            return;
        }
        // Scroll-drag.
        if (scrollCellH <= 0) return;
        if (!scrollMode) {
            const dy = t.clientY - touchStartY;
            const dx = t.clientX - touchStartX;
            if (Math.abs(dy) < DRAG_SLOP_PX) return;
            // Mostly-horizontal motion: ignore (no swipe-nav on terminal).
            if (Math.abs(dy) <= Math.abs(dx)) return;
            scrollEnter();
            scrollLastY = touchStartY;  // anchor scroll origin at gesture start
        }
        // Natural-scroll: pulling content DOWN reveals older lines (scroll
        // back). Multiplier turns the literal cell-per-pixel pacing into
        // something that feels like a real scrollview — a swipe of a few cm
        // moves through pages, not lines.
        const SCROLL_SPEED = 10;
        const rawRows = Math.trunc((t.clientY - scrollLastY) * SCROLL_SPEED / scrollCellH);
        if (rawRows === 0) return;
        // Finger moves DOWN (rawRows > 0) → reveal older content → scroll-up
        //                                    (negative pendingScrollRows).
        // Finger moves UP   (rawRows < 0) → reveal newer content → scroll-down.
        pendingScrollRows -= rawRows;
        scrollLastY += rawRows * scrollCellH / SCROLL_SPEED;
        pumpScroll();
    }, { passive: true });
    swipeTarget.addEventListener('touchend', (e) => {
        if (e.changedTouches.length !== 1) return;
        if (copyState && copyState.inMode && !scrollMode) {
            if (touchStartCell) {
                if (dragPhase === 'none' && !pendingMoveTarget) {
                    // Single tap — move cursor only (no selection).
                    scheduleMove(touchStartCell);
                } else {
                    // Drag finished — schedule one final move (initDrag will
                    // pick it up if still initializing; otherwise scheduleMove
                    // delivers it directly).
                    const t = e.changedTouches[0];
                    const cell = touchToCell(t);
                    if (dragPhase === 'extending') {
                        scheduleMove(cell);
                    } else {
                        pendingMoveTarget = cell;
                    }
                }
            }
            touchStartCell = null;
            dragGeom = null;
            return;
        }
        scrollEndDrag();
    }, { passive: true });

    // --- detach on hidden, reattach on visible ---
    // Tearing down the mirror when the tab is hidden releases atx's tmux
    // client so the pane snaps back to the user's mosh-only geometry.

    document.addEventListener('visibilitychange', () => {
        if (document.hidden) {
            if (connected) sendJSON({ type: 'view_hidden' });
            return;
        }
        if (!connected) {
            // WS was dropped while backgrounded — re-attach. onopen sends
            // a fresh `view`, so the server re-acquires the mirror and
            // repaints automatically.
            wantActiveOnNextView = true;
            connect();
            return;
        }
        fit.fit();
        const { cols, rows } = currentSize();
        sendJSON({ type: 'view', machine: view.machine, window: view.window, cols, rows, wantActive: true });
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
