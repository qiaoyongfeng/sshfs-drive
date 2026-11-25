package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// legacyConn is a connection as the Electron app saved it.
type legacyConn struct {
	UUID               string `json:"uuid"`
	Name               string `json:"name"`
	Host               string `json:"host"`
	Port               int    `json:"port"`
	User               string `json:"user"`
	Folder             string `json:"folder"`
	AuthType           string `json:"authType"`
	Password           string `json:"password"`
	KeyFile            string `json:"keyFile"`
	MountPoint         string `json:"mountPoint"`
	PreferredMountPoin string `json:"preferredMountPoint"`
	Advanced           struct {
		ConnectOnStartup bool `json:"connectOnStartup"`
	} `json:"advanced"`
}

// legacyFile is the shape of the Electron app's saved state: a Vuex store, or
// the flat configuration it was written from.
type legacyFile struct {
	Data struct {
		Connections []legacyConn `json:"connections"`
	} `json:"Data"`
	Settings struct {
		Settings legacySettings `json:"settings"`
	} `json:"Settings"`

	Connections []legacyConn   `json:"connections"`
	Flat        legacySettings `json:"settings"`
}

type legacySettings struct {
	SSHFSBinary        string `json:"sshfsBinary"`
	ProcessTimeout     int    `json:"processTrackTimeout"`
	TrayMessageOnClose *bool  `json:"displayTrayMessageOnClose"`
	StartupWithOS      *bool  `json:"startupWithOS"`
	ShowDebugPanel     *bool  `json:"showDebugPanel"`
}

// importLegacy reads the connections of SSHFS-Win Manager (the Electron app)
// the first time this app runs, so that upgrading loses nothing.
func importLegacy() ([]*Connection, Settings) {
	appData := os.Getenv("APPDATA")
	dirs := []string{
		filepath.Join(appData, "sshfs-win-manager"),
		filepath.Join(appData, "SSHFS-Win Manager"),
	}
	for _, dir := range dirs {
		for _, name := range []string{"vuex.json", "config.json", "state.json"} {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				continue
			}
			if conns, settings, ok := parseLegacy(data); ok {
				return conns, settings
			}
		}
	}
	return nil, defaultSettings()
}

// parseLegacy turns the old file into connections and settings.
func parseLegacy(data []byte) ([]*Connection, Settings, bool) {
	var f legacyFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, defaultSettings(), false
	}
	legacyConns := f.Data.Connections
	if len(legacyConns) == 0 {
		legacyConns = f.Connections
	}
	legacySet := f.Settings.Settings
	if legacySet.SSHFSBinary == "" {
		legacySet = f.Flat
	}

	settings := defaultSettings()
	if legacySet.ProcessTimeout > 0 {
		settings.ConnectTimeout = legacySet.ProcessTimeout
	}
	if legacySet.StartupWithOS != nil {
		settings.StartupWithOS = *legacySet.StartupWithOS
	}
	if legacySet.TrayMessageOnClose != nil {
		settings.TrayMessageOnClose = *legacySet.TrayMessageOnClose
	}
	if legacySet.ShowDebugPanel != nil {
		settings.ShowDebugPanel = *legacySet.ShowDebugPanel
	}

	conns := make([]*Connection, 0, len(legacyConns))
	for _, lc := range legacyConns {
		c := NewConnection()
		c.ID = lc.UUID
		if c.ID == "" {
			c.ID = newID()
		}
		c.Name = lc.Name
		c.Host = lc.Host
		if lc.Port > 0 {
			c.Port = lc.Port
		}
		c.User = lc.User
		if lc.Folder != "" {
			c.Folder = lc.Folder
		}
		switch AuthType(lc.AuthType) {
		case authPassword, authPasswordAsk, authKeyFile:
			c.Auth = AuthType(lc.AuthType)
		}
		c.Password = lc.Password
		if lc.KeyFile != "" {
			c.KeyFile = lc.KeyFile
		}
		if lc.MountPoint != "" {
			c.MountPoint = lc.MountPoint
		}
		c.MountedDrive = lc.PreferredMountPoin
		c.Advanced.ConnectOnStartup = lc.Advanced.ConnectOnStartup
		conns = append(conns, c)
	}
	return conns, settings, len(conns) > 0
}
