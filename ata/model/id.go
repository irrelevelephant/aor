package model

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

const base36Chars = "0123456789abcdefghijklmnopqrstuvwxyz"

// GenerateID creates a random base36 ID of the given length.
func GenerateID(length int) (string, error) {
	b := make([]byte, length)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(36))
		if err != nil {
			return "", fmt.Errorf("generate random: %w", err)
		}
		b[i] = base36Chars[n.Int64()]
	}
	return string(b), nil
}
