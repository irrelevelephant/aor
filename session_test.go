package main

import (
	"testing"
	"time"
)

func TestParseRateLimitReset(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantNil bool
		wantHr  int
		wantMin int
	}{
		{
			name:    "standard message with am",
			input:   "You're out of extra usage · resets 10am (America/Los_Angeles)",
			wantHr:  10,
			wantMin: 0,
		},
		{
			name:    "message with time and minutes",
			input:   "You're out of extra usage · resets 2:30pm (America/New_York)",
			wantHr:  14,
			wantMin: 30,
		},
		{
			name:    "smart quote apostrophe",
			input:   "You\u2019re out of extra usage \u00b7 resets 10am (America/Los_Angeles)",
			wantHr:  10,
			wantMin: 0,
		},
		{
			name:    "embedded in noisy output",
			input:   "[system] Session initialized                                   You're out of extra usage · resets 10am (America/Los_Angeles)  [08:45:01] WARNING: No structured status",
			wantHr:  10,
			wantMin: 0,
		},
		{
			name:    "no match",
			input:   "Everything is fine, no rate limit here",
			wantNil: true,
		},
		{
			name:    "UTC timezone",
			input:   "You're out of usage · resets 3pm (UTC)",
			wantHr:  15,
			wantMin: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseRateLimitReset(tt.input)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("expected nil, got %v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected non-nil time, got nil")
			}
			if got.Hour() != tt.wantHr {
				t.Errorf("hour = %d, want %d", got.Hour(), tt.wantHr)
			}
			if got.Minute() != tt.wantMin {
				t.Errorf("minute = %d, want %d", got.Minute(), tt.wantMin)
			}
			// Should be in the future (or today).
			if got.Before(time.Now().Add(-1 * time.Minute)) {
				t.Errorf("parsed time %v is in the past", got)
			}
		})
	}
}
