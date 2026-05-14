package runtime

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// dialSSH opens an SSH connection to host:22 as user. Auth precedence:
// ssh-agent (if SSH_AUTH_SOCK is set) → ~/.ssh/id_ed25519 → ~/.ssh/id_rsa.
//
// Host key checking is disabled because atx's trust boundary is Tailscale,
// not SSH host-key pinning — see plan §Open footnotes. The Tailscale tailnet
// authenticates the peer; SSH on top of that is just a transport.
func dialSSH(host, user string, timeout time.Duration) (*ssh.Client, error) {
	auths, err := loadAuthMethods()
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	if len(auths) == 0 {
		return nil, fmt.Errorf("no usable SSH auth (no agent, no id_ed25519/id_rsa)")
	}

	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            auths,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         timeout,
	}

	return ssh.Dial("tcp", net.JoinHostPort(host, "22"), cfg)
}

func loadAuthMethods() ([]ssh.AuthMethod, error) {
	var auths []ssh.AuthMethod

	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		conn, err := net.Dial("unix", sock)
		if err == nil {
			auths = append(auths, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return auths, err
	}
	for _, name := range []string{"id_ed25519", "id_rsa"} {
		data, err := os.ReadFile(filepath.Join(home, ".ssh", name))
		if err != nil {
			continue
		}
		signer, err := ssh.ParsePrivateKey(data)
		if err != nil {
			continue
		}
		auths = append(auths, ssh.PublicKeys(signer))
	}

	return auths, nil
}

