//go:build windows

package main

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// agentKeys are the keys the running ssh-agent offers, plus the way to let go
// of it once the connection has signed in.
type agentKeys struct {
	signers []ssh.Signer
	close   func()
}

// loadAgent reaches the OpenSSH agent Windows ships, which listens on a named
// pipe instead of a unix socket.
func loadAgent(logf func(string)) *agentKeys {
	const pipe = `\\.\pipe\openssh-ssh-agent`
	name, err := syscall.UTF16PtrFromString(pipe)
	if err != nil {
		return nil
	}
	h, err := syscall.CreateFile(name,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		0, nil, syscall.OPEN_EXISTING, 0, 0)
	if err != nil {
		return nil // the service is not running: keys come from the file
	}
	conn := os.NewFile(uintptr(h), pipe)
	client := agent.NewClient(conn)
	signers, err := client.Signers()
	if err != nil || len(signers) == 0 {
		_ = conn.Close()
		return nil
	}
	if logf != nil {
		logf(fmt.Sprintf("ssh: %d key(s) from ssh-agent", len(signers)))
	}
	return &agentKeys{signers: signers, close: func() { _ = conn.Close() }}
}
