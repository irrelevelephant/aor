//go:build !unix

package cmd

// StdinHasInput falls back to the TTY check on non-unix platforms. Less
// precise — a piped stdin with no producer would still block — but acceptable
// since agent-shell use is unix-centric.
func StdinHasInput() bool {
	return !isStdinTTY()
}
