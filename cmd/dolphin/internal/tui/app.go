package tui

import (
	"context"

	"github.com/rivo/tview"
	"github.com/moby/moby/client"
	"github.com/moby/moby/v2/cmd/dolphin/internal/mux"
)

// App encapsulates the global terminal UI state.
type App struct {
	UI     *tview.Application
	Pages  *tview.Pages
	Docker *client.Client
	Ctx    context.Context
	Cancel context.CancelFunc
}

// NewApp creates the TUI application root and injects the Docker client.
func NewApp(session *mux.Session) (*App, error) {
	ctx, cancel := context.WithCancel(context.Background())

	dockClient, err := NewMultiplexedDockerClient(session)
	if err != nil {
		cancel()
		return nil, err
	}

	app := &App{
		UI:     tview.NewApplication(),
		Pages:  tview.NewPages(),
		Docker: dockClient,
		Ctx:    ctx,
		Cancel: cancel,
	}

	// Wait, we need to initialize views after App is created
	// because views take *App as argument.
	return app, nil
}

// InitViews constructs the TUI views and routes them.
func (a *App) InitViews() {
	// Import views at the caller level to avoid circular imports if needed,
	// or we can just initialize them here. Wait, views imports tui.
	// This means we can't import views here (import cycle).
	// So we leave Pages empty here, and let the caller register views.
	a.UI.SetRoot(a.Pages, true).EnableMouse(true)
}

// Run starts the TUI event loop. Blocks until the app exits.
func (a *App) Run() error {
	defer a.Cancel()
	return a.UI.Run()
}

// Stop cleanly shuts down the TUI.
func (a *App) Stop() {
	a.Cancel()
	a.UI.Stop()
}
