package views

import (
	"context"
	"fmt"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/moby/moby/v2/cmd/dolphin/internal/tui"
)

type Dashboard struct {
	App        *tui.App
	Layout     *tview.Flex
	Table      *tview.Table
	Sidebar    *tview.List
	
	containers []container.Summary
	selected   map[string]bool
	
	OnSelect func(containerID string, name string)
}

func NewDashboard(app *tui.App) *Dashboard {
	d := &Dashboard{
		App:      app,
		Table:    tview.NewTable().SetSelectable(true, false).SetBorders(false),
		Sidebar:  tview.NewList().ShowSecondaryText(false),
		selected: make(map[string]bool),
	}

	d.Sidebar.AddItem("Containers", "", 'c', nil)
	d.Sidebar.AddItem("Images", "", 'i', nil)
	d.Sidebar.AddItem("Volumes", "", 'v', nil)
	d.Sidebar.AddItem("Networks", "", 'n', nil)
	d.Sidebar.SetBorder(true).SetTitle(" Main Menu ")

	d.Table.SetBorder(true).SetTitle(" Containers ")
	d.setupTableHeaders()

	// Keyboard bindings for the table
	d.Table.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyRune && event.Rune() == ' ' {
			row, _ := d.Table.GetSelection()
			if row > 0 && row <= len(d.containers) {
				c := d.containers[row-1]
				d.selected[c.ID] = !d.selected[c.ID]
				d.refreshTableData()
			}
			return nil
		}
		if event.Key() == tcell.KeyDelete || event.Key() == tcell.KeyBackspace2 || event.Key() == tcell.KeyBackspace {
			d.deleteSelected()
			return nil
		}
		if event.Key() == tcell.KeyEnter {
			row, _ := d.Table.GetSelection()
			if row > 0 && row <= len(d.containers) {
				c := d.containers[row-1]
				if d.OnSelect != nil {
					name := strings.TrimPrefix(c.Names[0], "/")
					d.OnSelect(c.ID, name)
				}
			}
			return nil
		}
		return event
	})

	footer := tview.NewTextView().SetText(" <Space> Toggle Select | <Delete> Remove Selected | <Enter> Open Details ").SetTextAlign(tview.AlignCenter)

	d.Layout = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(tview.NewFlex().
			AddItem(d.Sidebar, 20, 1, false).
			AddItem(d.Table, 0, 3, true), 0, 1, true).
		AddItem(footer, 1, 1, false)

	return d
}

func (d *Dashboard) setupTableHeaders() {
	d.Table.Clear()
	headers := []string{"[ ]", "NAME", "CONT. ID", "IMAGE", "STATE", "STATUS"}
	for i, h := range headers {
		d.Table.SetCell(0, i, tview.NewTableCell(h).SetTextColor(tcell.ColorYellow).SetSelectable(false))
	}
}

// Refresh hits the Yamux virtual Docker client and re-renders the table.
func (d *Dashboard) Refresh() {
	containers, err := d.App.Docker.ContainerList(d.App.Ctx, container.ListOptions{All: true})
	if err != nil {
		d.App.UI.QueueUpdateDraw(func() {
			d.Table.SetTitle(fmt.Sprintf(" Error: %v ", err))
		})
		return
	}
	d.App.UI.QueueUpdateDraw(func() {
		d.containers = containers
		d.refreshTableData()
	})
}

func (d *Dashboard) refreshTableData() {
	d.setupTableHeaders()
	for i, c := range d.containers {
		row := i + 1
		
		chk := "[ ]"
		if d.selected[c.ID] {
			chk = "[x]"
		}

		name := strings.TrimPrefix(c.Names[0], "/")
		shortID := c.ID
		if len(shortID) > 12 {
			shortID = shortID[:12]
		}

		color := tcell.ColorWhite
		if c.State == "running" {
			color = tcell.ColorGreen
		} else if c.State == "exited" {
			color = tcell.ColorGray
		}

		d.Table.SetCell(row, 0, tview.NewTableCell(chk).SetTextColor(tcell.ColorWhite))
		d.Table.SetCell(row, 1, tview.NewTableCell(name).SetTextColor(color))
		d.Table.SetCell(row, 2, tview.NewTableCell(shortID).SetTextColor(tcell.ColorWhite))
		d.Table.SetCell(row, 3, tview.NewTableCell(c.Image).SetTextColor(tcell.ColorWhite))
		d.Table.SetCell(row, 4, tview.NewTableCell(c.State).SetTextColor(color))
		d.Table.SetCell(row, 5, tview.NewTableCell(c.Status).SetTextColor(color))
	}
}

func (d *Dashboard) deleteSelected() {
	for id, selected := range d.selected {
		if selected {
			// Fire multiplexed delete
			go func(containerID string) {
				opts := container.RemoveOptions{Force: true}
				_ = d.App.Docker.ContainerRemove(context.Background(), containerID, opts)
				// Re-fetch after delete
				d.Refresh()
			}(id)
		}
	}
	d.selected = make(map[string]bool)
}
