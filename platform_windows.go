//go:build windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

var (
	kernel32             = syscall.NewLazyDLL("kernel32.dll")
	procGetLogicalDrives = kernel32.NewProc("GetLogicalDrives")
)

// defaultKeyFile is the key the add dialog starts from.
func defaultKeyFile() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.Getenv("USERPROFILE")
	}
	return filepath.Join(home, ".ssh", "id_rsa")
}

// freeDrive returns the first unused drive letter from D to Z, preferring the
// one the connection used last time.
func freeDrive(preferred string) (string, error) {
	mask, _, _ := procGetLogicalDrives.Call()
	used := func(letter byte) bool {
		bit := letter - 'A'
		return mask&(1<<bit) != 0
	}
	if len(preferred) > 0 {
		if l := preferred[0]; l >= 'A' && l <= 'Z' && !used(l) {
			return string(l) + ":", nil
		}
	}
	for l := byte('D'); l <= 'Z'; l++ {
		if !used(l) {
			return string(l) + ":", nil
		}
	}
	return "", errors.New("no free drive letter")
}
