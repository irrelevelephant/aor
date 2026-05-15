package web

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"aor/atx/db"
	"aor/atx/push"
)

// subscribeRequest matches what a browser's PushSubscription.toJSON()
// produces, with an extra user_agent for our records.
type subscribeRequest struct {
	Endpoint  string `json:"endpoint"`
	Keys      struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
	UserAgent string `json:"user_agent"`
}

type unsubscribeRequest struct {
	Endpoint string `json:"endpoint"`
}

// hookEvent is the body POSTed by Claude Code hook scripts.
type hookEvent struct {
	Machine     string `json:"machine"`
	Session     string `json:"session,omitempty"`
	WindowIndex string `json:"window_index,omitempty"`
	WindowName  string `json:"window_name,omitempty"`
	Event       string `json:"event"`              // "Notification" | "Stop"
	Message     string `json:"message,omitempty"`  // free-text summary
	Payload     any    `json:"payload,omitempty"`  // raw CC hook JSON, kept for debugging
}

func (s *Server) handleVAPIDPublicKey(w http.ResponseWriter, r *http.Request) {
	if s.vapid == nil {
		http.Error(w, "push not configured", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"key": s.vapid.PublicKey})
}

func (s *Server) handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	var req subscribeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Endpoint == "" || req.Keys.P256dh == "" || req.Keys.Auth == "" {
		http.Error(w, "endpoint and keys are required", http.StatusBadRequest)
		return
	}
	err := s.db.UpsertSubscription(db.Subscription{
		Endpoint:  req.Endpoint,
		P256dh:    req.Keys.P256dh,
		Auth:      req.Keys.Auth,
		UserAgent: req.UserAgent,
	})
	if err != nil {
		log.Printf("atx push subscribe: %v", err)
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	var req unsubscribeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Endpoint == "" {
		http.Error(w, "endpoint required", http.StatusBadRequest)
		return
	}
	if err := s.db.DeleteSubscriptionByEndpoint(req.Endpoint); err != nil {
		log.Printf("atx push unsubscribe: %v", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleHookEvent(w http.ResponseWriter, r *http.Request) {
	if s.vapid == nil {
		http.Error(w, "push not configured", http.StatusServiceUnavailable)
		return
	}
	var ev hookEvent
	if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if ev.Machine == "" || ev.Event == "" {
		http.Error(w, "machine and event are required", http.StatusBadRequest)
		return
	}

	// Log unconditionally. Step 9 will set Suppressed when a subscribed
	// viewer is currently looking at this window.
	if err := s.db.InsertNotification(db.Notification{
		Machine:     ev.Machine,
		Session:     ev.Session,
		WindowIndex: ev.WindowIndex,
		WindowName:  ev.WindowName,
		Event:       ev.Event,
		Message:     ev.Message,
	}); err != nil {
		log.Printf("atx hook log: %v", err)
	}

	subs, err := s.db.ListSubscriptions()
	if err != nil {
		log.Printf("atx hook list subs: %v", err)
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}

	payload := push.Payload{
		Title: notificationTitle(ev),
		Body:  notificationBody(ev),
		Tag:   fmt.Sprintf("atx:%s:%s", ev.Machine, ev.WindowIndex),
		Data: map[string]any{
			"url":     notificationURL(ev),
			"machine": ev.Machine,
			"window":  ev.WindowIndex,
		},
	}

	go s.fanOutPush(subs, payload)
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) fanOutPush(subs []db.Subscription, payload push.Payload) {
	for _, sub := range subs {
		res := push.Send(s.vapid, push.Subscription{
			Endpoint: sub.Endpoint,
			P256dh:   sub.P256dh,
			Auth:     sub.Auth,
		}, payload)
		if res.Gone {
			if err := s.db.DeleteSubscriptionByEndpoint(sub.Endpoint); err != nil {
				log.Printf("atx push prune %s: %v", sub.Endpoint, err)
			}
			continue
		}
		if res.Err != nil {
			log.Printf("atx push %s: %v", sub.Endpoint, res.Err)
		}
	}
}

func notificationTitle(ev hookEvent) string {
	switch ev.Event {
	case "Notification":
		return fmt.Sprintf("claude · %s · %s", ev.Machine, displayWindow(ev))
	case "Stop":
		return fmt.Sprintf("claude done · %s · %s", ev.Machine, displayWindow(ev))
	}
	return fmt.Sprintf("atx · %s · %s", ev.Machine, displayWindow(ev))
}

func notificationBody(ev hookEvent) string {
	if ev.Message != "" {
		return ev.Message
	}
	return ev.WindowName
}

func notificationURL(ev hookEvent) string {
	if ev.WindowIndex == "" {
		return fmt.Sprintf("/atx/m/%s", ev.Machine)
	}
	return fmt.Sprintf("/atx/m/%s/w/%s", ev.Machine, ev.WindowIndex)
}

func displayWindow(ev hookEvent) string {
	if ev.WindowIndex == "" {
		return ev.WindowName
	}
	if ev.WindowName == "" {
		return ev.WindowIndex
	}
	return ev.WindowIndex + " " + ev.WindowName
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
