//go:build unix

package cmd

import (
	"os"

	"golang.org/x/sys/unix"
)

// StdinHasInput reports whether a read from stdin would return immediately
// — because data is available, or because the peer has closed (EOF). Returns
// false for a TTY with no pending input or an open pipe with no producer,
// where io.ReadAll would block indefinitely.
func StdinHasInput() bool {
	fds := []unix.PollFd{{Fd: int32(os.Stdin.Fd()), Events: unix.POLLIN}}
	n, err := unix.Poll(fds, 0)
	if err != nil || n == 0 {
		return false
	}
	return fds[0].Revents&(unix.POLLIN|unix.POLLHUP) != 0
}
