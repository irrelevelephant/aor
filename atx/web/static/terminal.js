// Wires xterm.js to the per-window WebSocket at /atx/ws.
// Text frames carry JSON control messages; binary frames carry raw vt100
// bytes (stdout server→client, stdin client→server).

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
    fit.fit();
    term.focus();

    window.addEventListener('resize', () => fit.fit());

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

    ws.onopen = () => {
        connected = true;
        sendJSON({ type: 'hello' });
        sendJSON({ type: 'view', machine: view.machine, window: view.window });
        for (const msg of pending) ws.send(msg);
        pending = [];
    };

    ws.onmessage = (e) => {
        if (e.data instanceof ArrayBuffer) {
            term.write(new Uint8Array(e.data));
        } else if (typeof e.data === 'string') {
            // Control message; nothing actionable on this page yet beyond errors.
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

    term.onData((data) => {
        if (!connected) return;
        // xterm.js gives us strings of raw bytes (UTF-8 + control chars).
        // Send as binary so non-UTF-8 control sequences round-trip cleanly.
        ws.send(new TextEncoder().encode(data));
    });
})();
