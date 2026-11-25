//go:build !windows

package main

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// agentKeys are the keys the running ssh-agent offers, plus the way to let go
// of it once the connection has signed in.
type agentKeys struct {
	signers []ssh.Signer
	close   func()
}

// loadAgent talks to the agent SSH_AUTH_SOCK points at.
func loadAgent(logf func(string)) *agentKeys {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil
	}
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil
	}
	signers, err := agent.NewClient(conn).Signers()
	if err != nil || len(signers) == 0 {
		_ = conn.Close()
		return nil
	}
	if logf != nil {
		logf(fmt.Sprintf("ssh: %d key(s) from ssh-agent", len(signers)))
	}
	return &agentKeys{signers: signers, close: func() { _ = conn.Close() }}
}
