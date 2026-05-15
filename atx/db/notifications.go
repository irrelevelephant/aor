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

// LastNotifiedByWindow returns the most recent created_at (unix seconds)
// keyed by window_index for the given machine. Rows with an empty
// window_index are skipped.
func (d *DB) LastNotifiedByWindow(machine string) (map[string]int64, error) {
	rows, err := d.Query(`
		SELECT window_index, MAX(created_at)
		FROM notifications
		WHERE machine = ? AND window_index != ''
		GROUP BY window_index
	`, machine)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]int64)
	for rows.Next() {
		var idx string
		var ts int64
		if err := rows.Scan(&idx, &ts); err != nil {
			return nil, err
		}
		out[idx] = ts
	}
	return out, rows.Err()
}

// LastNotifiedByMachineWindow returns the most recent created_at (unix
// seconds) keyed by machine then window_index, in a single query — used
// by the unified machines view so we don't issue N+1 lookups when
// pre-rendering every machine's window list.
func (d *DB) LastNotifiedByMachineWindow() (map[string]map[string]int64, error) {
	rows, err := d.Query(`
		SELECT machine, window_index, MAX(created_at)
		FROM notifications
		WHERE window_index != ''
		GROUP BY machine, window_index
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]map[string]int64)
	for rows.Next() {
		var machine, idx string
		var ts int64
		if err := rows.Scan(&machine, &idx, &ts); err != nil {
			return nil, err
		}
		m, ok := out[machine]
		if !ok {
			m = make(map[string]int64)
			out[machine] = m
		}
		m[idx] = ts
	}
	return out, rows.Err()
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
