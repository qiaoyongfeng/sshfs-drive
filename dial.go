package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// connectSSH opens the SSH connection a mount rides on.
func connectSSH(c *Connection, password string, timeout time.Duration, logf func(string)) (*ssh.Client, error) {
	agent := loadAgent(logf)
	if agent != nil {
		defer agent.close()
	}

	auth, err := authMethods(c, password, agent, logf)
	if err != nil {
		return nil, err
	}

	addr := net.JoinHostPort(c.Host, itoa(portOf(c)))
	cfg := &ssh.ClientConfig{
		User:            c.User,
		Auth:            auth,
		HostKeyCallback: hostKeys(logf),
		Timeout:         timeout,
	}

	logf("ssh: dialing " + addr + " as " + c.User)
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, dialError(err, c, timeout)
	}
	logf("ssh: " + string(client.ServerVersion()))
	return client, nil
}

// authMethods builds what the connection signs in with: the password, a key
// file, or the keys the agent holds.
func authMethods(c *Connection, password string, agent *agentKeys, logf func(string)) ([]ssh.AuthMethod, error) {
	switch c.Auth {
	case authKeyFile:
		var signers []ssh.Signer
		if agent != nil {
			signers = append(signers, agent.signers...)
		}
		key, err := os.ReadFile(c.KeyFile)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("read %s: %w", filepath.Base(c.KeyFile), err)
		}
		if err == nil {
			signer, perr := ssh.ParsePrivateKey(key)
			if perr != nil {
				var locked *ssh.PassphraseMissingError
				if errors.As(perr, &locked) {
					if password == "" {
						return nil, fmt.Errorf("%s is encrypted: put its passphrase in the password field",
							filepath.Base(c.KeyFile))
					}
					signer, perr = ssh.ParsePrivateKeyWithPassphrase(key, []byte(password))
				}
			}
			if perr != nil {
				return nil, fmt.Errorf("%s: %w", filepath.Base(c.KeyFile), perr)
			}
			signers = append(signers, signer)
		}
		if len(signers) == 0 {
			return nil, fmt.Errorf("no key: %s is missing and ssh-agent holds none", c.KeyFile)
		}
		logf(fmt.Sprintf("ssh: %d key(s) to offer", len(signers)))
		return []ssh.AuthMethod{ssh.PublicKeys(signers...)}, nil

	default:
		if password == "" {
			return nil, errors.New("no password")
		}
		// Some servers only offer keyboard-interactive, others only
		// password; answering both costs nothing.
		interactive := ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
			answers := make([]string, len(questions))
			for i := range answers {
				answers[i] = password
			}
			return answers, nil
		})
		return []ssh.AuthMethod{ssh.Password(password), interactive}, nil
	}
}

// hostKeys checks the server against known_hosts when the user has the file.
// A host that is not in it is accepted, as sshfs was told to do, but the
// fingerprint is logged so it can be compared; a host whose key changed is
// refused, which is the case that matters.
func hostKeys(logf func(string)) ssh.HostKeyCallback {
	path := knownHostsPath()
	check, err := knownhosts.New(path)
	if err != nil {
		return func(hostname string, _ net.Addr, key ssh.PublicKey) error {
			logf("ssh: " + knownHostsNote() + ", accepting " + ssh.FingerprintSHA256(key))
			return nil
		}
	}
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := check(hostname, remote, key)
		if err == nil {
			return nil
		}
		var unknown *knownhosts.KeyError
		if errors.As(err, &unknown) && len(unknown.Want) == 0 {
			logf("ssh: new host key " + ssh.FingerprintSHA256(key) + ", accepted")
			return nil
		}
		return fmt.Errorf("the host key of %s changed: %w", hostname, err)
	}
}

func knownHostsPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".ssh", "known_hosts")
}

func knownHostsNote() string {
	if p := knownHostsPath(); p != "" {
		return "no " + filepath.Base(filepath.Dir(p)) + "/" + filepath.Base(p)
	}
	return "no known_hosts"
}

// dialError says what went wrong in words the dialog can show.
func dialError(err error, c *Connection, timeout time.Duration) error {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return fmt.Errorf("timed out after %s: check the host and the network", timeout)
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "unable to authenticate"):
		if c.Auth == authKeyFile {
			return errors.New("the server refused the key: wrong user, or the key is not authorized")
		}
		return errors.New("invalid user name or password")
	case strings.Contains(msg, "no supported methods remain"):
		return errors.New("the server offers no authentication method this app can use")
	case strings.Contains(msg, "connection refused"):
		return fmt.Errorf("connection refused: is sshd running on port %s?", itoa(portOf(c)))
	case strings.Contains(msg, "no such host"), strings.Contains(msg, "no such address"):
		return fmt.Errorf("cannot resolve %s", c.Host)
	}
	return err
}
