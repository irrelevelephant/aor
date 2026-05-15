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
        positionCopyCursor();
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
        }
    };

    ws.onclose = () => {
        connected = false;
        for (const p of pendingReqs.values()) p.reject(new Error('disconnected'));
        pendingReqs.clear();
        term.write('\r\n\x1b[33matx: connection closed\x1b[0m\r\n');
    };

    // --- modifier state machine + onData interception ---

    const modState = { ctrl: 'idle', alt: 'idle' }; // 'idle' | 'armed' | 'locked'

    function setMod(name, next) {
        modState[name] = next;
        for (const btn of document.querySelectorAll(`[data-action="mod:${name}"]`)) {
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
    // Visibility is CSS-driven: hidden by default, shown in
    // @media (pointer: coarse), (max-width: 600px). See style.css.

    // Resolved in JS so raw control bytes never round-trip through HTML
    // attribute parsing.
    const KEY_MAP = {
        esc:   '\x1b',
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

    function activateHbBtn(btn) {
        const action = btn.dataset.action;
        if (action === 'compose') { openPromptModal(); return; }
        if (action === 'copy')    { enterCopyMode(); return; }
        if (action === 'paste')   { pasteAction(); return; }
        if (action.startsWith('copyfn:')) {
            handleCopyFn(action.slice('copyfn:'.length));
            return;
        }
        const sep = action.indexOf(':');
        const kind = action.slice(0, sep);
        const name = action.slice(sep + 1);
        if (kind === 'mod') {
            const cur = modState[name];
            // Tap cycle: idle → armed → locked → idle.
            const next = cur === 'idle' ? 'armed' : cur === 'armed' ? 'locked' : 'idle';
            setMod(name, next);
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
    helperbar.addEventListener('pointerdown', (e) => {
        if (e.target.closest('.hb-btn')) e.preventDefault();
    });
    helperbar.addEventListener('pointerup', (e) => maybeFire(e.target.closest('.hb-btn')));
    helperbar.addEventListener('touchend', (e) => maybeFire(e.target.closest('.hb-btn')));
    helperbar.addEventListener('click', (e) => maybeFire(e.target.closest('.hb-btn')));

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
            if (!wasInMode) helperbar.setAttribute('data-mode', 'copy');
            if (copyButton) copyButton.classList.add('is-active');
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
        if (!copyState || !copyState.inMode) {
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

    // --- compose-prompt modal ---

    const promptModal = document.getElementById('prompt-modal');
    const promptTextarea = document.getElementById('prompt-modal-textarea');
    const promptClose = document.getElementById('prompt-modal-close');
    const promptSubmit = document.getElementById('prompt-modal-submit');

    function openPromptModal() {
        promptModal.hidden = false;
        // Defer focus to next frame so the keyboard pops up reliably on iOS.
        requestAnimationFrame(() => {
            promptTextarea.focus();
            dockBar();
        });
    }
    function closePromptModal() {
        promptModal.hidden = true;
        promptTextarea.value = '';
        promptSubmit.disabled = true;
        term.focus();
        dockBar();
    }
    function tryCloseWithConfirm() {
        if (promptTextarea.value.length > 0 && !confirm('Discard this prompt?')) return;
        closePromptModal();
    }

    promptTextarea.addEventListener('input', () => {
        promptSubmit.disabled = promptTextarea.value.length === 0;
    });
    promptClose.addEventListener('click', tryCloseWithConfirm);
    promptSubmit.addEventListener('click', () => {
        const text = promptTextarea.value;
        if (text.length === 0) return;
        sendBytes(text);
        closePromptModal();
    });

    // --- visualViewport docking: keep the helper bar (or the compose
    // modal's send button, when that modal is open) above the soft keyboard ---

    function dockBar() {
        if (!window.visualViewport) return;
        const vv = window.visualViewport;
        // Distance from layout-viewport bottom to visual-viewport bottom =
        // height of the keyboard (when shown).
        const liftedPx = Math.max(0, window.innerHeight - vv.height - vv.offsetTop);
        if (promptModal.hidden) {
            helperbar.style.transform = `translateY(${-liftedPx}px)`;
            document.body.style.setProperty('--helperbar-lift', `${liftedPx}px`);
            promptSubmit.style.transform = '';
            fit.fit();
            positionCopyCursor();
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
    const machineLabelEl = document.querySelector('.terminal-machine');
    const windowLabelEl = document.querySelector('.terminal-window-name');

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
    document.getElementById('terminal-nav-prev').addEventListener('click', () => navigateDelta(-1));
    document.getElementById('terminal-nav-next').addEventListener('click', () => navigateDelta(1));

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

    // --- touch on the terminal area: vertical drag scrolls scrollback,
    //     horizontal flick navigates prev/next window ---

    let touchStartX = 0, touchStartY = 0, touchStartT = 0;
    let scrollLastY = 0, scrollCellH = 0, didScroll = false;
    const SWIPE_MIN_X = 80, SWIPE_MAX_Y = 60, SWIPE_MAX_MS = 500;

    const swipeTarget = document.querySelector('.terminal-host');
    swipeTarget.addEventListener('touchstart', (e) => {
        if (e.touches.length !== 1) return;
        touchStartX = e.touches[0].clientX;
        touchStartY = e.touches[0].clientY;
        touchStartT = Date.now();
        didScroll = false;
        if (copyState && copyState.inMode) {
            dragGeom = cellGeom();
            touchStartCell = touchToCell(e.touches[0]);
            dragPhase = 'none';
            pendingMoveTarget = null;
            return;
        }
        scrollLastY = touchStartY;
        scrollCellH = term.element.getBoundingClientRect().height / Math.max(1, term.rows);
    }, { passive: true });
    swipeTarget.addEventListener('touchmove', (e) => {
        if (e.touches.length !== 1) return;
        const t = e.touches[0];
        if (copyState && copyState.inMode) {
            const dist = Math.hypot(t.clientX - touchStartX, t.clientY - touchStartY);
            if (dist < DRAG_SLOP_PX) return;
            didScroll = true;
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
        if (scrollCellH <= 0) return;
        const y = t.clientY;
        const rows = Math.trunc((y - scrollLastY) / scrollCellH);
        if (rows === 0) return;
        term.scrollLines(-rows);
        scrollLastY += rows * scrollCellH;
        didScroll = true;
    }, { passive: true });
    swipeTarget.addEventListener('touchend', (e) => {
        if (e.changedTouches.length !== 1) return;
        if (copyState && copyState.inMode) {
            if (didScroll) {
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
            } else if (touchStartCell) {
                // Single tap — move cursor only (no selection).
                scheduleMove(touchStartCell);
            }
            touchStartCell = null;
            dragGeom = null;
            return;
        }
        if (didScroll) return;
        const t = e.changedTouches[0];
        const dx = t.clientX - touchStartX;
        const dy = Math.abs(t.clientY - touchStartY);
        const dt = Date.now() - touchStartT;
        if (Math.abs(dx) < SWIPE_MIN_X || dy > SWIPE_MAX_Y || dt > SWIPE_MAX_MS) return;
        navigateDelta(dx > 0 ? -1 : 1);
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
