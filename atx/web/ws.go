package web

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"aor/atx/runtime"
)

// clientMsg is the inbound (browser → server) text-frame envelope.
// stdin uses binary frames, not this struct. `Payload` carries per-type
// fields (e.g. copy-mode coordinates) without bloating the top-level shape.
type clientMsg struct {
	Type       string          `json:"type"`
	Machine    string          `json:"machine,omitempty"`
	Window     int             `json:"window,omitempty"`
	Cols       uint32          `json:"cols,omitempty"`
	Rows       uint32          `json:"rows,omitempty"`
	ReqId      string          `json:"reqId,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	WantActive bool            `json:"wantActive,omitempty"`
}

// serverMsg is one outbound text-frame envelope. stdout uses binary frames.
type serverMsg struct {
	Type    string `json:"type"`
	Machine string `json:"machine,omitempty"`
	Window  int    `json:"window,omitempty"`
	Error   string `json:"error,omitempty"`
	ReqId   string `json:"reqId,omitempty"`
	Payload any    `json:"payload,omitempty"`
}

// Copy-mode payload shapes; tags match what terminal.js sends.

type copyMovePayload struct {
	FromRow int `json:"fromRow"`
	FromCol int `json:"fromCol"`
	ToRow   int `json:"toRow"`
	ToCol   int `json:"toCol"`
}

type copyActionPayload struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type pastePayload struct {
	Text string `json:"text"`
}

type copiedPayload struct {
	Text string `json:"text"`
}

type tmuxCmdPayload struct {
	Action string `json:"action"`
	Name   string `json:"name,omitempty"` // rename: new window name
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if s.rt == nil {
		http.Error(w, "runtime not configured", http.StatusServiceUnavailable)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Skip the WebSocket Origin-header check (NOT TLS verification —
		// this option's name is a historical misnomer). The tailnet is the
		// trust boundary, so any origin reaching this handler is acceptable.
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusInternalError, "closing")

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	c := &wsClient{
		conn: conn,
		rt:   s.rt,
		ctx:  ctx,
	}
	c.run()
}

// wsClient is one browser tab's WebSocket session. Only one window is
// "currently viewed" at a time; switching tears down the previous viewer
// before starting the next.
type wsClient struct {
	conn *websocket.Conn
	rt   *runtime.Manager
	ctx  context.Context

	mu       sync.Mutex
	machine  string
	windowIx int
	mirror   *runtime.Mirror // nil if not viewing
	output   chan []byte
	unsub    func() // unsubscribe from runtime state callbacks; nil when not viewing
	writeMu  sync.Mutex
}

func (c *wsClient) run() {
	defer c.stopViewing()
	for {
		typ, data, err := c.conn.Read(c.ctx)
		if err != nil {
			return
		}
		switch typ {
		case websocket.MessageText:
			c.handleControl(data)
		case websocket.MessageBinary:
			c.handleStdin(data)
		}
	}
}

func (c *wsClient) handleControl(data []byte) {
	var msg clientMsg
	if err := json.Unmarshal(data, &msg); err != nil {
		c.sendErrReq("", "invalid json")
		return
	}
	switch msg.Type {
	case "hello":
		// No state change; client is just announcing itself.
	case "view":
		if msg.Machine == "" {
			c.sendErrReq("", "view: machine required")
			return
		}
		c.startViewing(msg.Machine, msg.Window, msg.Cols, msg.Rows, msg.WantActive)
	case "view_hidden":
		c.stopViewing()
	case "resize":
		c.mu.Lock()
		machine := c.machine
		idx := c.windowIx
		mirror := c.mirror
		c.mu.Unlock()
		if mirror != nil && msg.Cols > 0 && msg.Rows > 0 {
			if err := c.rt.ResizeMirror(machine, idx, msg.Cols, msg.Rows); err != nil {
				log.Printf("atx ws: resize %s/w%d: %v", machine, idx, err)
			}
		}
	case "copy_enter":
		c.handleCopyEnter(msg.ReqId)
	case "copy_move":
		c.handleCopyMove(msg.ReqId, msg.Payload)
	case "copy_action":
		c.handleCopyAction(msg.ReqId, msg.Payload)
	case "copy_yank":
		c.handleCopyYank(msg.ReqId)
	case "copy_cancel":
		c.handleCopyCancel(msg.ReqId)
	case "paste_clipboard":
		c.handlePasteClipboard(msg.ReqId, msg.Payload)
	case "tmux_cmd":
		c.handleTmuxCmd(msg.ReqId, msg.Payload)
	}
}

func (c *wsClient) handleTmuxCmd(reqId string, raw json.RawMessage) {
	c.mu.Lock()
	mirror := c.mirror
	machine := c.machine
	c.mu.Unlock()
	if mirror == nil {
		c.sendErrReq(reqId, "not viewing")
		return
	}
	var p tmuxCmdPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		c.sendErrReq(reqId, "bad tmux_cmd payload")
		return
	}
	var windows []runtime.Window
	if snap, ok := c.rt.MachineState(machine); ok {
		windows = snap.Windows
	}
	res, err := mirror.WindowCommand(runtime.WindowAction(p.Action), p.Name, windows)
	if err != nil {
		c.sendErrReq(reqId, err.Error())
		return
	}
	// Sync-refresh the window list so the browser's follow-up
	// /atx/api/machines fetch sees the post-command state — otherwise
	// the picker/header race the 200ms tmux-event debounce and render
	// the old names/indexes.
	if err := c.rt.RefreshWindows(machine); err != nil {
		log.Printf("atx ws: refresh windows after %s: %v", p.Action, err)
	}
	c.writeJSON(serverMsg{Type: "tmux_cmd", ReqId: reqId, Payload: res})
}

func (c *wsClient) currentMirror() *runtime.Mirror {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mirror
}

func (c *wsClient) handleCopyEnter(reqId string) {
	mirror := c.currentMirror()
	if mirror == nil {
		c.sendErrReq(reqId, "not viewing")
		return
	}
	st, err := mirror.CopyEnter()
	if err != nil {
		c.sendErrReq(reqId, err.Error())
		return
	}
	c.sendCopyState(reqId, st)
}

func (c *wsClient) handleCopyMove(reqId string, raw json.RawMessage) {
	mirror := c.currentMirror()
	if mirror == nil {
		c.sendErrReq(reqId, "not viewing")
		return
	}
	var p copyMovePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		c.sendErrReq(reqId, "bad copy_move payload")
		return
	}
	st, err := mirror.CopyMove(p.FromRow, p.FromCol, p.ToRow, p.ToCol)
	if err != nil {
		c.sendErrReq(reqId, err.Error())
		return
	}
	c.sendCopyState(reqId, st)
}

func (c *wsClient) handleCopyAction(reqId string, raw json.RawMessage) {
	mirror := c.currentMirror()
	if mirror == nil {
		c.sendErrReq(reqId, "not viewing")
		return
	}
	var p copyActionPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		c.sendErrReq(reqId, "bad copy_action payload")
		return
	}
	st, err := mirror.CopyAction(p.Name, p.Count)
	if err != nil {
		c.sendErrReq(reqId, err.Error())
		return
	}
	c.sendCopyState(reqId, st)
}

func (c *wsClient) handleCopyYank(reqId string) {
	mirror := c.currentMirror()
	if mirror == nil {
		c.sendErrReq(reqId, "not viewing")
		return
	}
	text, err := mirror.CopyYank()
	if err != nil {
		c.sendErrReq(reqId, err.Error())
		return
	}
	c.sendCopied(reqId, text)
}

func (c *wsClient) handleCopyCancel(reqId string) {
	mirror := c.currentMirror()
	if mirror == nil {
		c.sendErrReq(reqId, "not viewing")
		return
	}
	st, err := mirror.CopyAction("cancel", 1)
	if err != nil {
		c.sendErrReq(reqId, err.Error())
		return
	}
	c.sendCopyState(reqId, st)
}

func (c *wsClient) handlePasteClipboard(reqId string, raw json.RawMessage) {
	mirror := c.currentMirror()
	if mirror == nil {
		c.sendErrReq(reqId, "not viewing")
		return
	}
	var p pastePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		c.sendErrReq(reqId, "bad paste_clipboard payload")
		return
	}
	if p.Text == "" {
		c.sendAckReq(reqId, "pasted")
		return
	}
	if err := mirror.PasteClipboard(p.Text); err != nil {
		c.sendErrReq(reqId, err.Error())
		return
	}
	c.sendAckReq(reqId, "pasted")
}

func (c *wsClient) handleStdin(data []byte) {
	c.mu.Lock()
	mirror := c.mirror
	machine := c.machine
	idx := c.windowIx
	c.mu.Unlock()
	if mirror == nil {
		return
	}
	if err := mirror.SendInput(data); err != nil {
		log.Printf("atx ws: stdin %s/w%d: %v", machine, idx, err)
	}
}

func (c *wsClient) startViewing(machine string, windowIdx int, cols, rows uint32, wantActive bool) {
	c.stopViewing()

	mirror, ch, err := c.rt.AcquireMirror(c.ctx, machine, windowIdx, cols, rows)
	if err != nil {
		c.sendErrReq("", err.Error())
		return
	}

	// Push an `active_window` frame whenever the session's focused window
	// drifts from what this tab is showing. Used both for the live
	// subscription (native tmux switches) and the one-shot snap below
	// (covers tab-was-hidden-while-user-switched).
	pushActive := func(snap runtime.MachineState) {
		c.mu.Lock()
		curMachine := c.machine
		curIdx := c.windowIx
		c.mu.Unlock()
		if curMachine != snap.Name || snap.ActiveWindow < 0 || snap.ActiveWindow == curIdx {
			return
		}
		c.writeJSON(serverMsg{Type: "active_window", Machine: snap.Name, Window: snap.ActiveWindow})
	}
	// Subscribe before storing fields so a callback firing immediately
	// still finds the right machine/window once the lock is taken.
	unsub := c.rt.Subscribe(machine, pushActive)

	c.mu.Lock()
	c.machine = machine
	c.windowIx = windowIdx
	c.mirror = mirror
	c.output = ch
	c.unsub = unsub
	c.mu.Unlock()

	// Snap to the session's current active window only when the client
	// asks for it (tab returning from hidden, post-reconnect). Picker /
	// arrow / swipe views must NOT snap or they'd bounce the user back
	// to whatever main is on.
	if wantActive {
		if snap, ok := c.rt.MachineState(machine); ok {
			pushActive(snap)
		}
	}

	// Resync any stale copy mode from a previous tab off the hot path —
	// it's one SSH round-trip (sometimes two) and the user's first
	// mirrored frame shouldn't wait for it.
	go func() {
		if st, err := mirror.CopyResync(); err == nil {
			c.sendCopyState("", st)
		}
	}()

	go c.forwardOutput(ch)
}

func (c *wsClient) stopViewing() {
	c.mu.Lock()
	machine := c.machine
	idx := c.windowIx
	ch := c.output
	unsub := c.unsub
	c.mirror = nil
	c.output = nil
	c.unsub = nil
	c.mu.Unlock()
	if unsub != nil {
		unsub()
	}
	if ch != nil {
		c.rt.ReleaseMirror(machine, idx, ch)
	}
}

func (c *wsClient) forwardOutput(ch chan []byte) {
	for data := range ch {
		writeCtx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
		c.writeMu.Lock()
		err := c.conn.Write(writeCtx, websocket.MessageBinary, data)
		c.writeMu.Unlock()
		cancel()
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				log.Printf("atx ws: write: %v", err)
			}
			return
		}
	}
}

func (c *wsClient) sendErrReq(reqId, msg string) {
	c.writeJSON(serverMsg{Type: "error", Error: msg, ReqId: reqId})
}

func (c *wsClient) sendCopyState(reqId string, st runtime.CopyState) {
	c.writeJSON(serverMsg{Type: "copy_state", ReqId: reqId, Payload: st})
}

func (c *wsClient) sendCopied(reqId, text string) {
	c.writeJSON(serverMsg{Type: "copied", ReqId: reqId, Payload: copiedPayload{Text: text}})
}

func (c *wsClient) sendAckReq(reqId, kind string) {
	c.writeJSON(serverMsg{Type: kind, ReqId: reqId})
}

func (c *wsClient) writeJSON(msg serverMsg) {
	payload, err := json.Marshal(msg)
	if err != nil {
		log.Printf("atx ws: marshal %s: %v", msg.Type, err)
		return
	}
	writeCtx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
	defer cancel()
	c.writeMu.Lock()
	c.conn.Write(writeCtx, websocket.MessageText, payload)
	c.writeMu.Unlock()
}
