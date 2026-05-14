package runtime

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

const sendKeysChunk = 4096

// inputChannel multiplexes per-window send-keys input over a single
// persistent `bash` SSH session, so we don't pay session-setup cost per
// keystroke. One channel per Machine; safe for concurrent callers via mu.
type inputChannel struct {
	session *ssh.Session
	stdin   io.WriteCloser
	mu      sync.Mutex
}

func startInputChannel(client *ssh.Client) (*inputChannel, error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		session.Close()
		return nil, err
	}
	session.Stderr = io.Discard
	session.Stdout = io.Discard
	if err := session.Start("bash"); err != nil {
		stdin.Close()
		session.Close()
		return nil, fmt.Errorf("start input bash: %w", err)
	}
	return &inputChannel{session: session, stdin: stdin}, nil
}

// SendKeys writes data to the given tmux target via `send-keys -l --`. Data
// is byte-transparent: xterm.js emits raw control bytes (e.g. \x03 for
// Ctrl-C) which we forward verbatim.
func (i *inputChannel) SendKeys(target string, data []byte) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	for len(data) > 0 {
		chunk := data
		if len(chunk) > sendKeysChunk {
			chunk = chunk[:sendKeysChunk]
		}
		cmd := fmt.Sprintf("tmux send-keys -t %s -l -- %s\n",
			shellQuote(target), shellQuoteBytes(chunk))
		if _, err := i.stdin.Write([]byte(cmd)); err != nil {
			return err
		}
		data = data[len(chunk):]
	}
	return nil
}

func (i *inputChannel) Close() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.stdin != nil {
		i.stdin.Close()
	}
	return i.session.Close()
}

// shellQuoteBytes is shellQuote but for arbitrary byte slices. All bytes are
// kept verbatim inside single quotes; embedded single quotes are escaped as
// the '...'\''...' pattern.
func shellQuoteBytes(b []byte) string {
	if len(b) == 0 {
		return "''"
	}
	return "'" + strings.ReplaceAll(string(b), "'", "'\\''") + "'"
}
