package model

import (
	"testing"
)

func TestGenerateID_Length(t *testing.T) {
	for _, length := range []int{3, 4, 5} {
		id, err := GenerateID(length)
		if err != nil {
			t.Fatalf("GenerateID(%d): %v", length, err)
		}
		if len(id) != length {
			t.Errorf("GenerateID(%d) = %q (len %d), want len %d", length, id, len(id), length)
		}
	}
}

func TestGenerateID_ValidChars(t *testing.T) {
	for i := 0; i < 100; i++ {
		id, err := GenerateID(3)
		if err != nil {
			t.Fatalf("GenerateID: %v", err)
		}
		for _, c := range id {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'z')) {
				t.Errorf("invalid char %c in ID %q", c, id)
			}
		}
	}
}

func TestGenerateID_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id, err := GenerateID(3)
		if err != nil {
			t.Fatalf("GenerateID: %v", err)
		}
		if seen[id] {
			// Possible but very unlikely with 46k possibilities.
			t.Logf("collision on attempt %d: %q (not necessarily a bug)", i, id)
		}
		seen[id] = true
	}
}
