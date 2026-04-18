package views

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/moby/moby/api/types"
	"github.com/moby/moby/api/types/container"
	dockerclient "github.com/moby/moby/client"
	"github.com/gdamore/tcell/v2"
	"github.com/guptarohit/asciigraph"
	"github.com/rivo/tview"
	"github.com/moby/moby/v2/cmd/dolphin/internal/tui"
)

type Details struct {
	App         *tui.App
	Layout      *tview.Flex
	Header      *tview.TextView
	Tabs        *tview.TextView
	LogsView    *tview.TextView
	StatsView   *tview.TextView
	
	containerID string
	cancelStream context.CancelFunc
	
	cpuHistory []float64
	memHistory []float64
}

func NewDetails(app *tui.App) *Details {
	d := &Details{
		App:        app,
		Header:     tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignCenter),
		Tabs:       tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignCenter),
		LogsView:   tview.NewTextView().SetDynamicColors(true).SetScrollable(true).SetMaxLines(1000),
		StatsView:  tview.NewTextView().SetDynamicColors(true),
		cpuHistory: make([]float64, 0),
		memHistory: make([]float64, 0),
	}

	d.LogsView.SetBorder(true).SetTitle(" Logs ")
	d.StatsView.SetBorder(true).SetTitle(" Live Stats ")

	d.Layout = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(d.Header, 2, 1, false).
		AddItem(d.Tabs, 1, 1, false).
		// By default show LogsView
		AddItem(d.LogsView, 0, 1, true)
		
	d.Tabs.SetText("[yellow][ Logs ][white]   < Stats >   [ Inspect ]")
	
	// Basic routing back to dashboard
	d.Layout.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEsc {
			d.Close()
			d.App.Pages.SwitchToPage("dashboard")
			return nil
		}
		// S to toggle stats view mapping
		if event.Key() == tcell.KeyRune && event.Rune() == 's' {
			d.Layout.RemoveItem(d.LogsView)
			d.Layout.AddItem(d.StatsView, 0, 1, true)
			d.App.UI.SetFocus(d.StatsView)
			return nil
		}
		if event.Key() == tcell.KeyRune && event.Rune() == 'l' {
			d.Layout.RemoveItem(d.StatsView)
			d.Layout.AddItem(d.LogsView, 0, 1, true)
			d.App.UI.SetFocus(d.LogsView)
			return nil
		}
		return event
	})

	return d
}

// Load begins streaming logs and network stats.
func (d *Details) Load(containerID, name string) {
	d.Close()
	d.containerID = containerID
	d.Header.SetText(fmt.Sprintf("[cyan]🐬 %s (%s) - Details", name, containerID[:12]))
	d.LogsView.Clear()
	d.StatsView.Clear()
	d.cpuHistory = make([]float64, 0)
	d.memHistory = make([]float64, 0)

	ctx, cancel := context.WithCancel(d.App.Ctx)
	d.cancelStream = cancel

	go d.streamLogs(ctx)
	go d.streamStats(ctx)
}

func (d *Details) Close() {
	if d.cancelStream != nil {
		d.cancelStream()
	}
}

func (d *Details) streamLogs(ctx context.Context) {
	opts := types.ContainerLogsOptions{ShowStdout: true, ShowStderr: true, Follow: true, Tail: "50"}
	reader, err := d.App.Docker.ContainerLogs(ctx, d.containerID, opts)
	if err != nil {
		d.App.UI.QueueUpdateDraw(func() { d.LogsView.SetText(err.Error()) })
		return
	}
	defer reader.Close()

	// tview's TextView is an io.Writer
	// We use a goroutine to continuously pipe
	buf := make([]byte, 1024)
	for {
		n, err := reader.Read(buf)
		if n > 0 {
			// Write the chunk to the TUI text view
			chunk := string(buf[:n])
			d.App.UI.QueueUpdateDraw(func() {
				fmt.Fprint(d.LogsView, chunk)
				d.LogsView.ScrollToEnd()
			})
		}
		if err != nil {
			if err != io.EOF && ctx.Err() == nil {
				d.App.UI.QueueUpdateDraw(func() { fmt.Fprintf(d.LogsView, "\n[red]Stream err: %v[white]\n", err) })
			}
			return
		}
	}
}

func (d *Details) streamStats(ctx context.Context) {
	resp, err := d.App.Docker.ContainerStats(ctx, d.containerID, dockerclient.ContainerStatsOptions{Stream: true})
	if err != nil {
		return
	}
	defer resp.Body.Close()

	decoder := json.NewDecoder(resp.Body)
	var stats struct {
		PreCPUStats struct {
			CPUUsage struct {
				TotalUsage uint64 `json:"total_usage"`
			} `json:"cpu_usage"`
			SystemCPUUsage uint64 `json:"system_cpu_usage"`
		} `json:"precpu_stats"`
		CPUStats struct {
			CPUUsage struct {
				TotalUsage uint64 `json:"total_usage"`
			} `json:"cpu_usage"`
			SystemCPUUsage uint64 `json:"system_cpu_usage"`
		} `json:"cpu_stats"`
		MemoryStats struct {
			Usage uint64 `json:"usage"`
			Limit uint64 `json:"limit"`
		} `json:"memory_stats"`
	}

	for {
		if err := decoder.Decode(&stats); err != nil {
			return
		}

		// Calculate approximate CPU %
		cpuDelta := float64(stats.CPUStats.CPUUsage.TotalUsage - stats.PreCPUStats.CPUUsage.TotalUsage)
		systemDelta := float64(stats.CPUStats.SystemCPUUsage - stats.PreCPUStats.SystemCPUUsage)
		cpuPercent := 0.0
		if systemDelta > 0.0 && cpuDelta > 0.0 {
			cpuPercent = (cpuDelta / systemDelta) * 100.0
		}
		
		memMB := float64(stats.MemoryStats.Usage) / 1024 / 1024

		// Draw ascii graphs
		d.App.UI.QueueUpdateDraw(func() {
			d.cpuHistory = append(d.cpuHistory, cpuPercent)
			d.memHistory = append(d.memHistory, memMB)
			
			if len(d.cpuHistory) > 40 { d.cpuHistory = d.cpuHistory[1:] }
			if len(d.memHistory) > 40 { d.memHistory = d.memHistory[1:] }

			if len(d.cpuHistory) == 0 { return }
			cpuGraph := asciigraph.Plot(d.cpuHistory, asciigraph.Height(10), asciigraph.Width(40), asciigraph.Caption("CPU Usage %"))
			memGraph := asciigraph.Plot(d.memHistory, asciigraph.Height(10), asciigraph.Width(40), asciigraph.Caption("RAM Usage MB"))
			
			d.StatsView.SetText(fmt.Sprintf("\n%s\n\n%s", cpuGraph, memGraph))
		})
	}
}
