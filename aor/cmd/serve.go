package cmd

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	aflcmd "aor/afl/cmd"
	afldb "aor/afl/db"
	aflweb "aor/afl/web"

	atacmd "aor/ata/cmd"
	atadb "aor/ata/db"
	ataweb "aor/ata/web"
)

// Hub is a shared SSE event broadcaster for both ata and afl.
type Hub struct {
	mu      sync.RWMutex
	clients map[chan string]struct{}
}

func newHub() *Hub {
	return &Hub{clients: make(map[chan string]struct{})}
}

func (h *Hub) Subscribe() chan string {
	ch := make(chan string, 64)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *Hub) Unsubscribe(ch chan string) {
	h.mu.Lock()
	delete(h.clients, ch)
	h.mu.Unlock()
	close(ch)
}

func (h *Hub) Broadcast(event, data string) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	msg := fmt.Sprintf("event: %s\ndata: %s\n\n", event, data)
	for ch := range h.clients {
		select {
		case ch <- msg:
		default:
			// Drop if client is slow.
		}
	}
}

func Serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	port := fs.Int("port", 4400, "HTTP port")
	addr := fs.String("addr", "0.0.0.0", "Listen address")
	tlsCert := fs.String("tls-cert", "", "Path to TLS certificate file")
	tlsKey := fs.String("tls-key", "", "Path to TLS private key file")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if (*tlsCert == "") != (*tlsKey == "") {
		return fmt.Errorf("--tls-cert and --tls-key must both be provided")
	}

	// Open ata database.
	ataDBPath, err := atadb.DefaultDBPath()
	if err != nil {
		return fmt.Errorf("ata db path: %w", err)
	}
	ataDB, err := atadb.Open(ataDBPath)
	if err != nil {
		return fmt.Errorf("open ata db: %w", err)
	}
	defer ataDB.Close()

	// Open afl database.
	aflDBPath, err := afldb.DefaultDBPath()
	if err != nil {
		return fmt.Errorf("afl db path: %w", err)
	}
	aflDB, err := afldb.Open(aflDBPath)
	if err != nil {
		return fmt.Errorf("open afl db: %w", err)
	}
	defer aflDB.Close()

	// Shared SSE hub.
	hub := newHub()

	// Create shared mux.
	mux := http.NewServeMux()

	// Register ata routes at / (existing behavior).
	ataweb.RegisterRoutes(mux, ataDB,
		ataweb.WithDispatch(atacmd.Dispatch),
		ataweb.WithSSE(hub),
	)

	// Register afl routes at /afl/.
	aflweb.RegisterRoutes(mux, aflDB,
		aflweb.WithDispatch(aflcmd.Dispatch),
		aflweb.WithSSE(hub),
	)

	// Shared SSE endpoint.
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", 500)
			return
		}

		ch := hub.Subscribe()
		defer hub.Unsubscribe(ch)

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		flusher.Flush()

		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case msg, ok := <-ch:
				if !ok {
					return
				}
				fmt.Fprint(w, msg)
				flusher.Flush()
			case <-ticker.C:
				fmt.Fprint(w, ":keepalive\n\n")
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	})

	// Recovery and body limit middleware.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("PANIC %s %s: %v", r.Method, r.URL, err)
				http.Error(w, "internal server error", 500)
			}
		}()
		if r.Method == "POST" && !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		}
		mux.ServeHTTP(w, r)
	})

	listen := fmt.Sprintf("%s:%d", *addr, *port)
	scheme := "http"
	if *tlsCert != "" {
		scheme = "https"
	}
	fmt.Printf("aor web server: %s://localhost:%d\n", scheme, *port)
	fmt.Printf("  ata UI:  %s://localhost:%d/\n", scheme, *port)
	fmt.Printf("  afl UI:  %s://localhost:%d/afl/\n", scheme, *port)
	fmt.Printf("  ata API: %s://localhost:%d/api/v1/exec\n", scheme, *port)
	fmt.Printf("  afl API: %s://localhost:%d/api/v1/afl/exec\n", scheme, *port)

	if *tlsCert != "" {
		return http.ListenAndServeTLS(listen, *tlsCert, *tlsKey, handler)
	}
	return http.ListenAndServe(listen, handler)
}
