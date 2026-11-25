package main

import "os"

// tracing reports whether calls should be logged. SSHFSMGR_LOG names a file to
// write them to, and implies tracing; SSHFSMGR_TRACE alone logs to the debug
// panel in the window.
func tracing() bool {
	return os.Getenv("SSHFSMGR_TRACE") != "" || os.Getenv("SSHFSMGR_LOG") != ""
}

// folderOf is the remote path a connection shows, "/" when it names none.
func folderOf(c *Connection) string {
	if c.Folder == "" {
		return "/"
	}
	return c.Folder
}

// portOf is the SSH port, 22 when the connection leaves it out.
func portOf(c *Connection) int {
	if c.Port <= 0 {
		return 22
	}
	return c.Port
}

// truncate shortens a label to n runes, for the volume name.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
