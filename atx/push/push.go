package push

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// Subscription is the minimum identity needed to deliver a push to one
// browser device. It maps to the JSON the PushSubscription.toJSON() method
// produces on the client side.
type Subscription struct {
	Endpoint string `json:"endpoint"`
	P256dh   string `json:"p256dh"`
	Auth     string `json:"auth"`
}

// Payload is what the service worker receives in `event.data.json()`.
type Payload struct {
	Title string         `json:"title"`
	Body  string         `json:"body,omitempty"`
	Tag   string         `json:"tag,omitempty"` // for OS-level dedupe
	Data  map[string]any `json:"data,omitempty"`
}

// Result reports the outcome of one Send call.
type Result struct {
	StatusCode int
	Gone       bool // 404/410 — subscription is dead and should be deleted
	Body       string
	Err        error
}

// Send delivers payload to one Subscription using the VAPID keypair.
func Send(v *VAPID, sub Subscription, payload Payload) Result {
	wpSub := &webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys: webpush.Keys{
			P256dh: sub.P256dh,
			Auth:   sub.Auth,
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return Result{Err: fmt.Errorf("marshal payload: %w", err)}
	}

	resp, err := webpush.SendNotification(body, wpSub, &webpush.Options{
		VAPIDPublicKey:  v.PublicKey,
		VAPIDPrivateKey: v.PrivateKey,
		Subscriber:      v.Subject,
		TTL:             30, // seconds: short enough that stale notifications die
	})
	if err != nil {
		return Result{Err: err}
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	r := Result{
		StatusCode: resp.StatusCode,
		Body:       string(respBody),
		Gone:       resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone,
	}
	if resp.StatusCode >= 400 && !r.Gone {
		r.Err = fmt.Errorf("push: %s: %s", resp.Status, r.Body)
	}
	return r
}
