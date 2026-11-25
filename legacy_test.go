package main

import "testing"

// The Electron app's saved state is read instead of starting empty.
func TestParseLegacyVuexState(t *testing.T) {
	data := []byte(`{
	  "Data": {"connections": [
	    {
	      "uuid": "0b9f-uuid", "name": "Home", "host": "192.168.1.20", "port": 2222,
	      "user": "ada", "folder": "/home/ada", "authType": "key-file",
	      "keyFile": "C:\\Users\\ada\\.ssh\\id_rsa", "mountPoint": "auto",
	      "preferredMountPoint": "M:",
	      "advanced": {
	        "connectOnStartup": true, "reconnect": true,
	        "customCmdlOptionsEnabled": true,
	        "customCmdlOptions": [{"name": "max_read", "value": "65536"}]
	      }
	    },
	    {"uuid": "", "name": "Old box", "host": "old", "user": "root",
	     "authType": "password", "password": "hunter2", "folder": "/"}
	  ]},
	  "Settings": {"settings": {
	    "sshfsBinary": "C:\\Program Files\\SSHFS-Win\\bin\\sshfs.exe",
	    "processTrackTimeout": 30, "startupWithOS": false,
	    "displayTrayMessageOnClose": false, "showDebugPanel": true
	  }}
	}`)

	conns, settings, ok := parseLegacy(data)
	if !ok || len(conns) != 2 {
		t.Fatalf("ok %v, %d connections", ok, len(conns))
	}
	home := conns[0]
	if home.ID != "0b9f-uuid" || home.Name != "Home" || home.Port != 2222 {
		t.Errorf("home: %+v", home)
	}
	if home.Auth != authKeyFile || home.MountedDrive != "M:" {
		t.Errorf("home: %+v", home)
	}
	if !home.Advanced.ConnectOnStartup {
		t.Errorf("home advanced: %+v", home.Advanced)
	}
	if conns[1].ID == "" {
		t.Error("a connection without a uuid kept an empty id")
	}
	if settings.ConnectTimeout != 30 || settings.StartupWithOS || settings.TrayMessageOnClose || !settings.ShowDebugPanel {
		t.Errorf("settings: %+v", settings)
	}
}

// A flat file, and anything unreadable, are handled too.
func TestParseLegacyFlatAndBad(t *testing.T) {
	flat := []byte(`{"connections":[{"name":"a","host":"h","user":"u","authType":"password"}],
	  "settings":{"sshfsBinary":"C:\\x\\sshfs.exe","processTrackTimeout":5}}`)
	conns, settings, ok := parseLegacy(flat)
	if !ok || len(conns) != 1 || settings.ConnectTimeout != 5 {
		t.Fatalf("flat: ok %v, %d connections, %+v", ok, len(conns), settings)
	}
	if conns[0].Port != 22 || conns[0].MountPoint != "auto" || conns[0].Folder != "/" {
		t.Errorf("defaults were not filled in: %+v", conns[0])
	}

	if _, _, ok := parseLegacy([]byte("not json")); ok {
		t.Error("garbage parsed as a configuration")
	}
	if _, _, ok := parseLegacy([]byte(`{"Data":{"connections":[]}}`)); ok {
		t.Error("an empty state counts as an import")
	}
}
