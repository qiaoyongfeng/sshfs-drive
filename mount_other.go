//go:build !windows

package main

import "errors"

// mounter mounts connections as drives. A drive letter is a Windows idea, so
// elsewhere the app keeps its connections and says why it cannot mount them.
type mounter struct {
	logf func(string)
}

type mount struct {
	conn  *Connection
	drive string
}

func (m *mounter) Mount(c *Connection, s Settings, password string) (*mount, error) {
	return nil, errors.New("mounting is only supported on Windows")
}

func (mt *mount) Wait() error { return nil }

func (mt *mount) Unmount() error { return nil }

// Drive is the mount point the connection would have taken.
func (mt *mount) Drive() string { return mt.drive }
