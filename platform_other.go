//go:build !windows

package main

import (
	"os"
	"path/filepath"
)

// defaultKeyFile is the key the add dialog starts from.
func defaultKeyFile() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = "."
	}
	return filepath.Join(home, ".ssh", "id_rsa")
}

// freeDrive picks a mount point: elsewhere a directory to mount on.
func freeDrive(preferred string) (string, error) {
	if preferred != "" && filepath.IsAbs(preferred) {
		return preferred, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "mnt"), nil
}
