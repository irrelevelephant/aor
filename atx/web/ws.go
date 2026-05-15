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
// stdin uses binary frames, not this struct.
type clientMsg struct {
	Type    string `json:"type"`
	Machine string `json:"machine,omitempty"`
	Window  int    `json:"window,omitempty"`
	Cols    uint32 `json:"cols,omitempty"`
	Rows    uint32 `json:"rows,omitempty"`
}

// serverMsg is one outbound text-frame envelope. stdout uses binary frames.
type serverMsg struct {
	Type    string `json:"type"`
	Machine string `json:"machine,omitempty"`
	Window  int    `json:"window,omitempty"`
	Error   string `json:"error,omitempty"`
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
		c.sendErr("invalid json")
		return
	}
	switch msg.Type {
	case "hello":
		// No state change; client is just announcing itself.
	case "view":
		if msg.Machine == "" {
			c.sendErr("view: machine required")
			return
		}
		c.startViewing(msg.Machine, msg.Window, msg.Cols, msg.Rows)
	case "view_hidden":
		c.stopViewing()
	case "resize":
		c.mu.Lock()
		machine := c.machine
		idx := c.windowIx
		hasView := c.mirror != nil
		c.mu.Unlock()
		if hasView && msg.Cols > 0 && msg.Rows > 0 {
			if err := c.rt.ResizeMirror(machine, idx, msg.Cols, msg.Rows); err != nil {
				log.Printf("atx ws: resize %s/w%d: %v", machine, idx, err)
			}
		}
	}
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

func (c *wsClient) startViewing(machine string, windowIdx int, cols, rows uint32) {
	c.stopViewing()

	mirror, ch, err := c.rt.AcquireMirror(c.ctx, machine, windowIdx, cols, rows)
	if err != nil {
		c.sendErr(err.Error())
		return
	}

	c.mu.Lock()
	c.machine = machine
	c.windowIx = windowIdx
	c.mirror = mirror
	c.output = ch
	c.mu.Unlock()

	go c.forwardOutput(ch)
}

func (c *wsClient) stopViewing() {
	c.mu.Lock()
	machine := c.machine
	idx := c.windowIx
	ch := c.output
	c.mirror = nil
	c.output = nil
	c.mu.Unlock()
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

func (c *wsClient) sendErr(msg string) {
	payload, _ := json.Marshal(serverMsg{Type: "error", Error: msg})
	writeCtx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
	defer cancel()
	c.writeMu.Lock()
	c.conn.Write(writeCtx, websocket.MessageText, payload)
	c.writeMu.Unlock()
}
