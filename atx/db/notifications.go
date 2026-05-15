package db

import "time"

// Notification is one row of the notifications log: every hook event,
// whether it was delivered or suppressed.
type Notification struct {
	ID          int64
	Machine     string
	Session     string
	WindowIndex string
	WindowName  string
	Event       string
	Message     string
	Suppressed  bool
	CreatedAt   int64
}

// InsertNotification appends one row.
func (d *DB) InsertNotification(n Notification) error {
	suppressed := 0
	if n.Suppressed {
		suppressed = 1
	}
	if n.CreatedAt == 0 {
		n.CreatedAt = time.Now().Unix()
	}
	_, err := d.Exec(`
		INSERT INTO notifications
		  (machine, session, window_index, window_name, event, message, suppressed, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, n.Machine, n.Session, n.WindowIndex, n.WindowName, n.Event, n.Message, suppressed, n.CreatedAt)
	return err
}
