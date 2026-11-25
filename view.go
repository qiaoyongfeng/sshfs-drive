package main

import (
	"github.com/egoist/mygo/ui"
)

// view draws the window from the app's state.
func (a *app) view(c *ui.Context) {
	if !a.editOpen && c.Shortcut(ui.Cmd, ui.KeyN) {
		a.addConnection()
	}
	if !a.settingsOpen && c.Shortcut(ui.Cmd, ui.KeyComma) {
		a.settingsDraft = a.store.Settings()
		a.settingsOpen = true
	}

	for _, message := range a.toasts {
		c.Toast(message)
	}
	a.toasts = a.toasts[:0]

	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		a.mainPane(c)
		a.sidebar(c)
	})

	a.editDialog(c)
	a.settingsDialog(c)
	a.aboutDialog(c)
	a.askDialog(c)
	a.confirmDialog(c)
}

// mainPane is the connection list and, below it, the debug output.
func (a *app) mainPane(c *ui.Context) {
	ui.Column(c).Grow(1).Children(func() {
		conns := a.connections()
		ui.Row(c).Padding(14, 18, 6, 18).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Connections").FontSize(16).Bold()
			if len(conns) > 0 {
				ui.Badge(c, itoa(len(conns)))
			}
			ui.Spacer(c)
		})
		a.connectionList(c)
		if a.store.Settings().ShowDebugPanel {
			a.debugPanel(c)
		}
	})
}

// connectionList builds the rows, which drag to reorder.
func (a *app) connectionList(c *ui.Context) {
	conns := a.connections()
	a.list.Key = func(i int) any { return conns[i].ID }
	a.list.Reorder = func(rows []int, to int) {
		a.store.Move(rows, to)
	}
	list := ui.List(c, &a.list, len(conns)).Grow(1).Rows(func(row ui.ListRow) {
		a.connectionRow(row, conns[row.Index])
	})
	list.Children(func() {
		if len(conns) == 0 {
			a.emptyState(c)
		}
	})
}

// emptyState invites the first connection.
func (a *app) emptyState(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Center().Gap(10).Children(func() {
		ui.Icon(c, iconServer).FontSize(40).TextColor(t.TextMuted.Alpha(0.5))
		ui.Text(c, "No connections yet").FontSize(15).Bold()
		ui.Text(c, "Add a server to mount its files as a drive.").FontSize(12).TextColor(t.TextMuted)
		if ui.PrimaryButton(c, "").PaddingX(16).Height(34).Children(func() {
			ui.Icon(c, iconPlus).FontSize(15)
			ui.Text(c, "Add Connection").SingleLine()
		}).Clicked() {
			a.addConnection()
		}
	})
}

// connectionRow is one server: what it is, where it is mounted, and what can
// be done with it.
func (a *app) connectionRow(row ui.ListRow, conn *Connection) {
	c := row.Context
	t := c.Theme()
	bg := ui.Transparent
	if row.Hovered() {
		bg = t.SurfaceHover
	}

	detail := conn.User + "@" + conn.Host
	if conn.Folder != "" && conn.Folder != "/" {
		detail += " · " + conn.Folder
	}

	ui.Row(c).Padding(8, 16).Gap(12).AlignItems(ui.Center).Background(bg).Children(func() {
		tile := ui.Box(c).Size(40, 40).Radius(10).AlignItems(ui.Center).Justify(ui.Center)
		switch conn.Status {
		case statusConnected:
			tile.Background(t.Success.Alpha(0.14))
		case statusConnecting, statusClosing:
			tile.Background(t.Warning.Alpha(0.14))
		default:
			tile.Background(t.SurfaceHover)
		}
		tile.Children(func() {
			ui.Icon(c, iconServer).FontSize(19).TextColor(statusColor(t, conn.Status))
		})

		ui.Column(c).Grow(1).Gap(3).Children(func() {
			ui.Text(c, nameOf(conn)).FontSize(13.5).Bold().SingleLine()
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Padding(1, 7).Radius(999).Background(t.Accent.Alpha(0.12)).Children(func() {
					ui.Text(c, conn.MountLabel()).FontSize(10.5).Bold().TextColor(t.Accent)
				})
				ui.Text(c, detail).FontSize(11.5).TextColor(t.TextMuted).SingleLine()
			})
		})

		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			if conn.Status == statusConnected {
				iconButton(c, iconFolder, "Open "+conn.MountLabel()).OnClick(func() { a.openMount(conn) })
			}
			if conn.Status == statusConnecting || conn.Status == statusClosing {
				ui.Box(c).Size(34, 34).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
					ui.Spinner(c).Size(16, 16).TextColor(t.TextMuted)
				})
			} else if conn.Status == statusConnected {
				iconButton(c, iconPlug, "Disconnect").Background(t.Success.Alpha(0.14)).
					TextColor(t.Success).OnClick(func() { a.disconnect(conn) })
			} else {
				iconButton(c, iconPlug, "Connect").OnClick(func() { a.connect(conn) })
			}
			iconButton(c, iconDots, "More").Menu(func(m *ui.Menu) {
				if m.Item("Edit…").Chosen() {
					a.editConnection(conn)
				}
				if m.Item("Duplicate").Chosen() {
					a.duplicateConnection(conn)
				}
				if m.Item("Open mount").Disabled(conn.Status != statusConnected).Chosen() {
					a.openMount(conn)
				}
				m.Separator()
				if m.Item("Delete…").Disabled(conn.Status == statusClosing).Chosen() {
					a.confirm = conn
					a.confirmOpen = true
				}
			})
		})
	})
}

// statusColor tints a connection's icon by what it is doing.
func statusColor(t *ui.Theme, s Status) ui.Color {
	switch s {
	case statusConnected:
		return t.Success
	case statusConnecting, statusClosing:
		return t.Warning
	}
	return t.TextMuted
}

// iconButton is a compact button that shows an icon and names itself.
func iconButton(c *ui.Context, icon *ui.SVG, label string) ui.Element {
	return ui.Button(c, "").Label(label).Tooltip(label).Width(32).Height(32).Radius(8).
		Children(func() {
			ui.Icon(c, icon).FontSize(16)
		})
}

// sidebar holds the actions that are not about one connection.
func (a *app) sidebar(c *ui.Context) {
	t := c.Theme()
	s := a.store.Settings()
	mounted := 0
	for _, conn := range a.connections() {
		if conn.Status == statusConnected {
			mounted++
		}
	}

	ui.Column(c).Width(200).AlignItems(ui.Stretch).Padding(12).Gap(4).
		Background(t.Surface).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).
		Children(func() {
			if ui.PrimaryButton(c, "").Height(34).Tooltip("Add Connection (Ctrl+N)").Children(func() {
				ui.Icon(c, iconPlus).FontSize(15)
				ui.Text(c, "Add Connection").SingleLine()
			}).Clicked() {
				a.addConnection()
			}

			ui.Box(c).Height(10)

			sidebarButton(c, iconGear, "Settings", func() {
				a.settingsDraft = a.store.Settings()
				a.settingsOpen = true
			})
			sidebarButton(c, iconTerm, toggleLabel(s.ShowDebugPanel), func() { a.toggleDebug() })
			sidebarButton(c, iconInfo, "About", func() { a.aboutOpen = true })

			ui.Spacer(c)

			status := itoa(mounted) + " of " + itoa(len(a.connections())) + " mounted"
			ui.Text(c, status).FontSize(11).TextColor(t.TextMuted).TextAlign(ui.Start).
				Padding(0, 4, 2, 4).FillWidth()
		})
}

func toggleLabel(on bool) string {
	if on {
		return "Hide debug output"
	}
	return "Show debug output"
}

// sidebarButton is one action in the sidebar.
func sidebarButton(c *ui.Context, icon *ui.SVG, label string, onClick func()) {
	ui.Button(c, "").Height(32).Radius(7).Justify(ui.Start).PaddingX(8).Gap(8).
		Children(func() {
			ui.Icon(c, icon).FontSize(15)
			ui.Text(c, label).FontSize(12).SingleLine()
		}).OnClick(onClick)
}

// debugPanel shows what the mounts are doing.
func (a *app) debugPanel(c *ui.Context) {
	t := c.Theme()
	lines := a.logs.Lines()

	ui.Column(c).Height(190).BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
		ui.Row(c).Padding(8, 14, 2, 14).Gap(4).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "DEBUG OUTPUT").FontSize(9.5).Bold().TextColor(t.TextMuted)
			ui.Spacer(c)
			iconButton(c, iconCopy, "Copy debug output").OnClick(func() {
				c.WriteClipboard(a.logs.String())
				a.toast("Debug output copied to the clipboard")
			})
			iconButton(c, iconTrash, "Clear debug output").OnClick(func() { a.logs.Clear() })
			iconButton(c, iconClose, "Hide debug output").OnClick(func() { a.toggleDebug() })
		})

		a.logList.FollowEnd = true
		ui.List(c, &a.logList, len(lines)).Grow(1).Rows(func(row ui.ListRow) {
			ui.Text(row.Context, lines[row.Index]).Font("Consolas").FontSize(11).
				TextColor(t.Text).SingleLine().Padding(0, 14)
		}).Children(func() {
			if len(lines) == 0 {
				ui.Text(c, "Nothing yet: connect to a server to see the mount's log here.").
					FontSize(11).TextColor(t.TextMuted).Padding(2, 14, 8, 14)
			}
		})
	})
}
