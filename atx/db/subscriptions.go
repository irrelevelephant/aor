package db

import "time"

type Subscription struct {
	ID         string
	Endpoint   string
	P256dh     string
	Auth       string
	UserAgent  string
	LastSeenAt int64
}

// UpsertSubscription inserts a new push_subscriptions row keyed by
// endpoint, or refreshes the keys + last_seen_at on conflict.
func (d *DB) UpsertSubscription(s Subscription) error {
	now := time.Now().Unix()
	if s.ID == "" {
		id, err := d.randomID()
		if err != nil {
			return err
		}
		s.ID = id
	}
	_, err := d.Exec(`
		INSERT INTO push_subscriptions (id, endpoint, p256dh, auth, user_agent, created_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(endpoint) DO UPDATE SET
			p256dh = excluded.p256dh,
			auth = excluded.auth,
			user_agent = excluded.user_agent,
			last_seen_at = excluded.last_seen_at
	`, s.ID, s.Endpoint, s.P256dh, s.Auth, s.UserAgent, now, now)
	return err
}

// ListSubscriptions returns every registered subscription.
func (d *DB) ListSubscriptions() ([]Subscription, error) {
	rows, err := d.Query(`
		SELECT id, endpoint, p256dh, auth, COALESCE(user_agent, ''), last_seen_at
		FROM push_subscriptions
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Subscription
	for rows.Next() {
		var s Subscription
		if err := rows.Scan(&s.ID, &s.Endpoint, &s.P256dh, &s.Auth, &s.UserAgent, &s.LastSeenAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeleteSubscriptionByEndpoint removes a row by endpoint. Idempotent.
func (d *DB) DeleteSubscriptionByEndpoint(endpoint string) error {
	_, err := d.Exec(`DELETE FROM push_subscriptions WHERE endpoint = ?`, endpoint)
	return err
}

// randomID returns 16 hex chars (8 random bytes) using sqlite's randomblob
// so we don't pull in a separate UUID dep.
func (d *DB) randomID() (string, error) {
	var id string
	err := d.QueryRow(`SELECT lower(hex(randomblob(8)))`).Scan(&id)
	return id, err
}
