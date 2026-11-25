package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/egoist/mygo"
)

// AuthType is how a connection authenticates with the SSH server.
type AuthType string

const (
	authPassword    AuthType = "password"
	authPasswordAsk AuthType = "password-ask"
	authKeyFile     AuthType = "key-file"
)

// Status is what a connection's mount is doing right now.
type Status string

const (
	statusDisconnected Status = "disconnected"
	statusConnecting   Status = "connecting"
	statusConnected    Status = "connected"
	statusClosing      Status = "disconnecting"
)

// Advanced holds a connection's advanced settings.
type Advanced struct {
	ConnectOnStartup bool `json:"connectOnStartup"`
	// ReadOnly mounts the drive without letting anything change the server.
	ReadOnly bool `json:"readOnly"`
}

// Connection is one SSHFS mount the user saved.
type Connection struct {
	ID       string   `json:"uuid"`
	Name     string   `json:"name"`
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	User     string   `json:"user"`
	Folder   string   `json:"folder"`
	Auth     AuthType `json:"authType"`
	Password string   `json:"password,omitempty"`
	KeyFile  string   `json:"keyFile"`
	// MountPoint is "auto" or a drive letter such as "M:".
	MountPoint string `json:"mountPoint"`
	// MountedDrive is the drive letter an "auto" mount ended up using, so
	// the list can keep showing it across launches.
	MountedDrive string   `json:"preferredMountPoint,omitempty"`
	Advanced     Advanced `json:"advanced"`

	// Status is what the mount is doing right now, never saved.
	Status Status `json:"-"`

	// draft is set while the add/edit dialog edits a copy of this connection.
	draft *Connection
}

// MountLabel is the drive badge the list shows for the connection.
func (c *Connection) MountLabel() string {
	if c.MountedDrive != "" && (c.MountPoint == "auto" || c.MountPoint == "") {
		return c.MountedDrive
	}
	if c.MountPoint == "" || c.MountPoint == "auto" {
		return "auto"
	}
	return c.MountPoint
}

// Target is the user@host:folder a connection points at.
func (c *Connection) Target() string {
	port := ""
	if c.Port > 0 && c.Port != 22 {
		port = ":" + itoa(c.Port)
	}
	folder := c.Folder
	if folder == "" {
		folder = "/"
	}
	return c.User + "@" + c.Host + port + ":" + folder
}

// Settings are the application's preferences.
type Settings struct {
	// ConnectTimeout is how many seconds a mount may take to come up.
	ConnectTimeout int `json:"connectTimeout"`
	// CacheTimeout is how many seconds an attribute stays good, which trades
	// server round trips for seeing another machine's changes later.
	CacheTimeout       int  `json:"cacheTimeout"`
	StartupWithOS      bool `json:"startupWithOS"`
	TrayMessageOnClose bool `json:"displayTrayMessageOnClose"`
	ShowDebugPanel     bool `json:"showDebugPanel"`
}

func defaultSettings() Settings {
	return Settings{
		ConnectTimeout:     15,
		CacheTimeout:       20,
		StartupWithOS:      false,
		TrayMessageOnClose: true,
	}
}

// NewConnection returns a connection with the defaults the add dialog starts
// from.
func NewConnection() *Connection {
	return &Connection{
		ID:         newID(),
		Name:       "",
		Host:       "",
		Port:       22,
		User:       "",
		Folder:     "/",
		Auth:       authPassword,
		KeyFile:    defaultKeyFile(),
		MountPoint: "auto",
		Status:     statusDisconnected,
	}
}

// newID returns a short random identifier for a connection.
func newID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return itoa(int(os.Getpid()))
	}
	return hex.EncodeToString(b[:])
}

// Clone returns a copy that editing can change without touching the original.
func (c *Connection) Clone() *Connection {
	dup := *c
	dup.Status = c.Status
	return &dup
}

// file is what the store writes to disk.
type file struct {
	Connections []*Connection `json:"connections"`
	Settings    Settings      `json:"settings"`
}

// Store keeps the connections and settings, and saves them to the user data
// directory. Mutations run on the main thread, like the views that read it.
type Store struct {
	mu       sync.Mutex
	path     string
	conns    []*Connection
	settings Settings
	onSave   func()
}

// OpenStore loads the configuration, creating the defaults on first run.
func OpenStore() (*Store, error) {
	s := &Store{settings: defaultSettings()}
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil || dir == "" {
		return s, err
	}
	s.path = filepath.Join(dir, "config.json")
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		s.conns, s.settings = importLegacy()
		if len(s.conns) > 0 {
			s.Save()
		}
		return s, nil
	}
	if err != nil {
		return s, err
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return s, err
	}
	s.conns = f.Connections
	s.settings = mergeSettings(f.Settings)
	for _, c := range s.conns {
		c.Status = statusDisconnected
		if c.Port == 0 {
			c.Port = 22
		}
		if c.Folder == "" {
			c.Folder = "/"
		}
		if c.MountPoint == "" {
			c.MountPoint = "auto"
		}
	}
	return s, nil
}

// mergeSettings fills in fields an older configuration file lacks.
func mergeSettings(in Settings) Settings {
	def := defaultSettings()
	if in.ConnectTimeout <= 0 {
		in.ConnectTimeout = def.ConnectTimeout
	}
	if in.CacheTimeout <= 0 {
		in.CacheTimeout = def.CacheTimeout
	}
	return in
}

// Path is where the configuration file lives.
func (s *Store) Path() string { return s.path }

// Connections returns the saved connections.
func (s *Store) Connections() []*Connection {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns
}

// Settings returns the preferences.
func (s *Store) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

// SetSettings replaces the preferences and saves them.
func (s *Store) SetSettings(v Settings) {
	s.mu.Lock()
	s.settings = v
	s.mu.Unlock()
	s.Save()
}

// Find returns the connection with id, or nil.
func (s *Store) Find(id string) *Connection {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.conns {
		if c.ID == id {
			return c
		}
	}
	return nil
}

// Add appends a connection and saves the list.
func (s *Store) Add(c *Connection) {
	s.mu.Lock()
	s.conns = append(s.conns, c)
	s.mu.Unlock()
	s.Save()
}

// Update replaces the connection that has the same ID.
func (s *Store) Update(c *Connection) {
	s.mu.Lock()
	for i, old := range s.conns {
		if old.ID == c.ID {
			c.Status = old.Status
			s.conns[i] = c
			break
		}
	}
	s.mu.Unlock()
	s.Save()
}

// Remove deletes a connection and saves the list.
func (s *Store) Remove(id string) {
	s.mu.Lock()
	out := s.conns[:0]
	for _, c := range s.conns {
		if c.ID != id {
			out = append(out, c)
		}
	}
	s.conns = out
	s.mu.Unlock()
	forgetPassword(id)
	s.Save()
}

// Move reorders the connections: rows (sorted) land before index to, or at the
// end when to equals the length.
func (s *Store) Move(rows []int, to int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(rows) == 0 {
		return
	}
	moved := make([]*Connection, 0, len(rows))
	rest := make([]*Connection, 0, len(s.conns))
	picked := make(map[int]bool, len(rows))
	for _, r := range rows {
		if r >= 0 && r < len(s.conns) {
			picked[r] = true
		}
	}
	for i, c := range s.conns {
		if picked[i] {
			moved = append(moved, c)
		} else {
			rest = append(rest, c)
		}
	}
	// to counts positions in the list without the moved rows.
	if to > len(rest) {
		to = len(rest)
	}
	if to < 0 {
		to = 0
	}
	out := make([]*Connection, 0, len(s.conns))
	out = append(out, rest[:to]...)
	out = append(out, moved...)
	out = append(out, rest[to:]...)
	s.conns = out
}

// Save writes the configuration to disk.
func (s *Store) Save() {
	s.mu.Lock()
	path := s.path
	settings := s.settings
	conns := make([]*Connection, len(s.conns))
	copy(conns, s.conns)
	s.mu.Unlock()

	for _, c := range conns {
		// "Ask on connect" keeps its password for that mount alone.
		if c.Password != "" && c.Auth != authPasswordAsk && keepPassword(c.ID, c.Password) {
			c.Password = "" // in the credential store instead of the file
		}
	}
	if path == "" {
		return
	}
	data, err := json.MarshalIndent(file{Connections: conns, Settings: settings}, "", "  ")
	if err != nil {
		return
	}
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// password loads the connection's password from the credential store when the
// file does not hold it.
func (s *Store) password(c *Connection) string {
	if c.Password != "" {
		return c.Password
	}
	pw, err := mygo.Secrets.Get(secretKey(c.ID))
	if err != nil {
		return ""
	}
	c.Password = string(pw)
	return c.Password
}

// keepPassword stores the password in the system's credential store, and
// reports whether it is there now, so that the file need not keep it.
func keepPassword(id, password string) bool {
	if id == "" {
		return false
	}
	return mygo.Secrets.Set(secretKey(id), []byte(password)) == nil
}

// forgetPassword removes a deleted connection's password.
func forgetPassword(id string) {
	_ = mygo.Secrets.Delete(secretKey(id))
}

// secretKey names a connection's password in the credential store.
func secretKey(id string) string { return "sshfs-conn-" + id }

// itoa is a tiny helper so the model needs no fmt import.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
