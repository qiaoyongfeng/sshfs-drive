package main

import (
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// authLabels are the authentication choices, in the order the menu shows
// them.
var authLabels = []string{
	"Password",
	"Password (ask on connect)",
	"Private key file",
}

func authLabel(t AuthType) string {
	switch t {
	case authPasswordAsk:
		return authLabels[1]
	case authKeyFile:
		return authLabels[2]
	}
	return authLabels[0]
}

func authFromLabel(label string) AuthType {
	switch label {
	case authLabels[1]:
		return authPasswordAsk
	case authLabels[2]:
		return authKeyFile
	}
	return authPassword
}

// drives lists what the drive menu offers: automatic, or a letter.
func drives() []string {
	out := []string{"auto"}
	for l := byte('D'); l <= 'Z'; l++ {
		out = append(out, string(l)+":")
	}
	return out
}

// editDialog adds a connection or changes one.
func (a *app) editDialog(c *ui.Context) {
	t := c.Theme()
	ui.Modal(c, &a.editOpen, func() {
		d := a.draft
		if d == nil {
			return
		}
		title := "Add Connection"
		if a.editing {
			title = "Edit Connection"
		}
		ui.Column(c).Width(560).Padding(20).Gap(12).Radius(14).
			Background(t.Background).Border(1, t.Border).Children(func() {
			ui.Text(c, title).FontSize(16).Bold()
			ui.Tabs(c, &a.tab, "Basic", "Advanced")
			if a.tab == 0 {
				a.basicFields(c, d)
			} else {
				a.advancedFields(c, d)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					a.editOpen = false
				}
				if ui.PrimaryButton(c, "Save").Clicked() {
					a.touched = true
					if a.draftError() == "" {
						a.saveDraft()
					}
				}
			})
			if message := a.draftError(); message != "" {
				ui.Text(c, message).FontSize(11).TextColor(t.Danger)
			}
		})
	})
	if !a.editOpen {
		a.draft = nil
	}
}

// draftError says what keeps the connection from being saved, if anything.
func (a *app) draftError() string {
	d := a.draft
	if d == nil || !a.touched {
		return ""
	}
	switch {
	case strings.TrimSpace(d.Name) == "":
		return "Give the connection a name."
	case strings.TrimSpace(d.Host) == "":
		return "Enter the server's host name or address."
	case strings.TrimSpace(d.User) == "":
		return "Enter the user to sign in as."
	case d.Auth == authKeyFile && strings.TrimSpace(d.KeyFile) == "":
		return "Choose the private key to sign in with."
	}
	return ""
}

// basicFields is the first tab: where the server is and how to sign in.
func (a *app) basicFields(c *ui.Context, d *Connection) {
	t := c.Theme()
	port := float64(d.Port)
	auth := authLabel(d.Auth)

	ui.Form(c, func() {
		ui.Field(c, "Name", func() {
			ui.TextInput(c, &d.Name).Placeholder("My development server")
		})

		ui.Field(c, "Server", func() {
			ui.Row(c).Gap(8).Children(func() {
				ui.TextInput(c, &d.Host).Placeholder("example.com or 10.0.0.4").Grow(1)
				ui.NumberInput(c, &port, 1, 65535, 1).Width(110).Label("Port").Tooltip("Port")
			})
		}).Description("The port is 22 unless the server listens elsewhere.")

		ui.Field(c, "User", func() {
			ui.TextInput(c, &d.User).Placeholder("root")
		})

		ui.Field(c, "Sign in with", func() {
			ui.Select(c, &auth, authLabels)
		})
		d.Auth = authFromLabel(auth)

		if d.Auth == authPasswordAsk {
			ui.Field(c, "Password", func() {
				ui.Text(c, "Asked for every time the connection is mounted.").
					FontSize(11).TextColor(t.TextMuted)
			})
		} else if d.Auth == authPassword {
			ui.Field(c, "Password", func() {
				ui.TextInput(c, &d.Password).Password().Placeholder("Stored in the Windows credential store")
			})
		} else {
			ui.Field(c, "Key file", func() {
				ui.Row(c).Gap(6).Children(func() {
					ui.TextInput(c, &d.KeyFile).Grow(1)
					iconButton(c, iconKey, "Choose key file").OnClick(func() {
						files, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
							Title:       "Select private key",
							DefaultPath: d.KeyFile,
						})
						if err == nil && len(files) > 0 {
							d.KeyFile = files[0]
						}
					})
				})
			})
		}

		ui.Field(c, "Remote path", func() {
			ui.TextInput(c, &d.Folder).Placeholder("/home/user")
		})

		ui.Field(c, "Drive letter", func() {
			ui.Select(c, &d.MountPoint, drives())
		}).Description("Auto picks the first free drive each time it connects.")
	})

	d.Port = int(port)
}

// advancedFields is the second tab: when to connect and what the mount allows.
func (a *app) advancedFields(c *ui.Context, d *Connection) {
	t := c.Theme()

	ui.Column(c).Gap(10).Children(func() {
		ui.Form(c, func() {
			ui.Field(c, "On startup", func() {
				ui.Switch(c, &d.Advanced.ConnectOnStartup)
			}).Description("Mount when " + appName + " starts.")

			ui.Field(c, "Read-only", func() {
				ui.Switch(c, &d.Advanced.ReadOnly)
			}).Description("Show the files without letting anything change them.")
		})

		ui.Divider(c).Margin(4, 0)

		ui.Text(c, "Everything runs in this app: the SSH connection, the SFTP session "+
			"and the drive itself, so no sshfs.exe or ssh.exe is involved. The server "+
			"needs its SFTP subsystem, which OpenSSH has on by default.").
			FontSize(11).TextColor(t.TextMuted).FillWidth()
	})
}

// settingsDialog changes how the app behaves.
func (a *app) settingsDialog(c *ui.Context) {
	t := c.Theme()
	ui.Modal(c, &a.settingsOpen, func() {
		s := a.settingsDraft
		timeout := float64(s.ConnectTimeout)
		cache := float64(s.CacheTimeout)

		ui.Column(c).Width(540).Padding(20).Gap(12).Radius(14).
			Background(t.Background).Border(1, t.Border).Children(func() {
			ui.Text(c, "Settings").FontSize(16).Bold()

			ui.Form(c, func() {
				ui.Field(c, "Connect timeout", func() {
					ui.NumberInput(c, &timeout, 1, 300, 1).Width(110)
				}).Description("Seconds to wait for a mount before giving up.")

				ui.Field(c, "Cache lifetime", func() {
					ui.NumberInput(c, &cache, 0, 3600, 5).Width(110)
				}).Description("Seconds an attribute stays good. Higher means fewer round trips.")

				ui.Field(c, "Start with Windows", func() {
					ui.Switch(c, &s.StartupWithOS)
				})
				ui.Field(c, "Tray reminder", func() {
					ui.Switch(c, &s.TrayMessageOnClose)
				}).Description("Say that the app is still in the tray when the window closes.")
				ui.Field(c, "Debug panel", func() {
					ui.Switch(c, &s.ShowDebugPanel)
				}).Description("Show what the mounts are doing under the list.")
			})

			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					a.settingsOpen = false
				}
				if ui.PrimaryButton(c, "OK").Clicked() {
					s.ConnectTimeout = int(timeout)
					s.CacheTimeout = int(cache)
					a.store.SetSettings(s)
					a.applyLoginItem()
					a.logList.FollowEnd = true
					a.settingsOpen = false
					a.toast("Settings saved")
				}
			})
		})

		a.settingsDraft = s
	})
}

// aboutDialog says what this is.
func (a *app) aboutDialog(c *ui.Context) {
	t := c.Theme()
	ui.Modal(c, &a.aboutOpen, func() {
		ui.Column(c).Width(420).Padding(22).Gap(10).Radius(14).
			Background(t.Background).Border(1, t.Border).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, iconServer).FontSize(30).TextColor(t.Accent)
				ui.Column(c).Gap(2).Children(func() {
					ui.Text(c, appName).FontSize(16).Bold()
					ui.Text(c, "Version "+version).FontSize(11.5).TextColor(t.TextMuted)
				})
			})
			ui.Text(c, "Mounts SSH servers as drives on Windows, speaking SSH and SFTP itself.").
				FontSize(12).TextColor(t.Text)
			ui.Text(c, "MyGo draws the window; WinFsp turns the files into a drive. One Go binary, no sshfs.exe, no Electron.").
				FontSize(11.5).TextColor(t.TextMuted)
			ui.Divider(c).Margin(4, 0)
			ui.Text(c, "Inspired by SSHFS-Win Manager, the Electron app it replaces.").
				FontSize(10.5).TextColor(t.TextMuted)
			ui.Row(c).Gap(16).Children(func() {
				ui.Link(c, "SSHFS-Win Manager", "https://github.com/evsar3/sshfs-win-manager")
				ui.Link(c, "WinFsp", "https://github.com/winfsp/winfsp")
				ui.Link(c, "MyGo", "https://github.com/egoist/mygo")
			})
			// WinFsp's FLOSS exception asks for this notice in the interface
			// itself, and the drive cannot exist without it.
			ui.Text(c, "WinFsp - Windows File System Proxy, Copyright (C) Bill Zissimopoulos").
				FontSize(10).TextColor(t.TextMuted).FillWidth()
			ui.Row(c).Justify(ui.End).Children(func() {
				if ui.PrimaryButton(c, "Close").Clicked() {
					a.aboutOpen = false
				}
			})
		})
	})
}

// askDialog asks for the password of a connection that wants one each time.
func (a *app) askDialog(c *ui.Context) {
	t := c.Theme()
	ui.Modal(c, &a.askOpen, func() {
		conn := a.ask
		if conn == nil {
			return
		}
		submit := func() {
			conn.Password = a.askText
			conn.Status = statusConnecting
			a.askOpen = false
			a.mountAsync(conn)
		}
		ui.Column(c).Width(400).Padding(20).Gap(12).Radius(14).
			Background(t.Background).Border(1, t.Border).Children(func() {
			ui.Text(c, "Password for "+conn.User+"@"+conn.Host).FontSize(14).Bold()
			if ui.TextInput(c, &a.askText).Password().AutoFocus().Submitted() {
				submit()
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, "Cancel").Clicked() {
					conn.Status = statusDisconnected
					a.askOpen = false
				}
				if ui.PrimaryButton(c, "Connect").Clicked() {
					submit()
				}
			})
		})
	})
	if !a.askOpen {
		a.ask = nil
	}
}

// confirmDialog asks before a connection is deleted.
func (a *app) confirmDialog(c *ui.Context) {
	conn := a.confirm
	if conn == nil {
		return
	}
	switch ui.AlertDialog(c, &a.confirmOpen, "Delete “"+nameOf(conn)+"”?",
		"Its mount is closed and its settings are removed. This can't be undone.",
		"Cancel", "Delete") {
	case 1:
		if a.mounted(conn.ID) {
			a.disconnect(conn)
		}
		a.store.Remove(conn.ID)
		a.confirm = nil
	}
	if !a.confirmOpen {
		a.confirm = nil
	}
}
