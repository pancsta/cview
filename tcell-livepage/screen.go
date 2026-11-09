// Package tcelllivepage provides a tcell screen which renders to a browser as
// HTML streamed by livepage. DOM input events are mapped back to tcell cell
// coordinates.
package tcelllivepage

import (
	"context"
	"html"
	"net/http"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	lp "github.com/pancsta/livepage"
)

const (
	// DefaultCols and DefaultRows are the initial grid dimensions, used until
	// the browser reports its viewport.
	DefaultCols = 80
	DefaultRows = 25

	// DefaultFontSize is the cell font size in CSS pixels.
	DefaultFontSize = 14

	// maxDim guards against abusive resize messages.
	maxDim = 4096

	screenID = "tcell-screen"
)

// Theme defines the default colors of the screen.
type Theme struct {
	Fg     tcell.Color
	Bg     tcell.Color
	Cursor tcell.Color
}

// DefaultTheme is a dark theme.
func DefaultTheme() Theme {
	return Theme{
		Fg:     tcell.NewHexColor(0xD4D4D4),
		Bg:     tcell.NewHexColor(0x101014),
		Cursor: tcell.NewHexColor(0x56B6C2),
	}
}

// Opts configures a [Screen].
type Opts struct {
	// Cols and Rows are the initial grid dimensions before the browser reports
	// the viewport. Defaults to [DefaultCols] and [DefaultRows].
	Cols, Rows int
	// FontSize is the CSS font size in pixels. Defaults to [DefaultFontSize].
	FontSize int
	// FontFamily is the CSS font family. Defaults to a monospace stack.
	FontFamily string
	// Theme defines the default colors. Defaults to [DefaultTheme].
	Theme Theme
	// Title is the title of the default HTML page. Optional.
	Title string
	// RefreshInterval coalesces [Screen.Show] refreshes to at most this rate,
	// using a trailing refresh. Zero means no throttling. Use it for
	// applications which draw frequently, e.g. 100ms for ~10fps.
	RefreshInterval time.Duration
	// Live holds the livepage options. HtmlPage is wrapped to inject the
	// screen's CSS and JS. Debounce defaults to 20ms.
	Live lp.Opts
}

// Screen is a tcell screen which streams its grid to the browser as HTML using
// livepage. It embeds a [tcell.SimulationScreen], so it can be passed to
// [cview.Application.SetScreen] or used by any tcell application.
//
// The screen is initialized by [NewScreen]; [tcell.Screen.Init] is a no-op.
type Screen struct {
	tcell.SimulationScreen

	ctx    context.Context
	cancel context.CancelFunc

	page  *lp.Page[*compo]
	compo *compo
	mach  *am.Machine

	theme      Theme
	fontSize   int
	fontFamily string

	refreshInterval time.Duration
	// primed is set after the first refresh, so the initial frame is rendered
	// even without any clients.
	primed       bool
	lastRefresh  time.Time
	refreshTimer *time.Timer

	mu     sync.Mutex
	dirty  bool
	html   string
	styles map[tcell.Style]string
	// gen is incremented on every screen update.
	gen uint64

	// sizesMx guards the per-browser viewports.
	sizesMx sync.Mutex
	sizes   map[string][2]int

	finiOnce sync.Once
}

// NewScreen creates an initialized screen. The page lives as long as ctx; the
// returned screen disposes it on [Screen.Fini].
func NewScreen(ctx context.Context, opts Opts) (*Screen, error) {
	if opts.Cols <= 0 {
		opts.Cols = DefaultCols
	}
	if opts.Rows <= 0 {
		opts.Rows = DefaultRows
	}
	if opts.FontSize <= 0 {
		opts.FontSize = DefaultFontSize
	}
	if opts.FontFamily == "" {
		opts.FontFamily = `ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace`
	}
	def := DefaultTheme()
	if !opts.Theme.Fg.Valid() {
		opts.Theme.Fg = def.Fg
	}
	if !opts.Theme.Bg.Valid() {
		opts.Theme.Bg = def.Bg
	}
	if !opts.Theme.Cursor.Valid() {
		opts.Theme.Cursor = def.Cursor
	}
	if opts.Live.Debounce <= 0 {
		opts.Live.Debounce = 20 * time.Millisecond
	}

	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		return nil, err
	}
	// The default fill char of a simulation screen is 'X'; emulate a blank
	// terminal before the first draw.
	sim.SetSize(opts.Cols, opts.Rows)
	sim.Clear()
	sim.Show()
	sim.EnableMouse()

	ctx, cancel := context.WithCancel(ctx)
	s := &Screen{
		SimulationScreen: sim,
		ctx:              ctx,
		cancel:           cancel,
		theme:            opts.Theme,
		fontSize:         opts.FontSize,
		fontFamily:       opts.FontFamily,
		refreshInterval:  opts.RefreshInterval,
		dirty:            true,
		styles:           map[tcell.Style]string{},
		sizes:            map[string][2]int{},
	}

	// Wrap the page template to inject the screen's CSS and JS. When no
	// template is provided, use a full-page one.
	fullPage := opts.Live.HtmlPage == nil
	basePage := opts.Live.HtmlPage
	if basePage == nil {
		title := opts.Title
		if title == "" {
			title = "livepage"
		}
		basePage = func(body string) string {
			return "<!DOCTYPE html><html><head><meta charset=\"utf-8\">" +
				"<title>" + html.EscapeString(title) + "</title></head><body>" +
				body + "</body></html>"
		}
	}
	opts.Live.HtmlPage = func(body string) string {
		return injectHead(basePage(body), s.headTags(fullPage))
	}
	// Keep a user-provided custom event hook.
	userOnEvent := opts.Live.OnEvent
	opts.Live.OnEvent = func(ev *lp.DomEvent) {
		s.handleEvent(ev)
		if userOnEvent != nil {
			userOnEvent(ev)
		}
	}
	// Keep a user-provided client hook.
	userOnClient := opts.Live.OnClient
	opts.Live.OnClient = func(id string, connected bool) {
		s.handleClient(id, connected)
		if userOnClient != nil {
			userOnClient(id, connected)
		}
	}

	s.compo = &compo{s: s}
	mach := am.New(ctx, am.Schema{am.StateReady: {}}, nil)
	s.mach = mach
	page, err := lp.New(ctx, s.compo, opts.Live)
	if err != nil {
		cancel()
		sim.Fini()
		mach.Dispose()
		return nil, err
	}
	s.page = page

	return s, nil
}

// Init is a no-op, the screen is initialized by [NewScreen].
func (s *Screen) Init() error {
	return nil
}

// Fini stops the screen and disposes the page.
func (s *Screen) Fini() {
	s.finiOnce.Do(func() {
		s.mu.Lock()
		if s.refreshTimer != nil {
			s.refreshTimer.Stop()
			s.refreshTimer = nil
		}
		s.mu.Unlock()

		s.SimulationScreen.Fini()
		s.page.Dispose()
		s.cancel()
		s.mach.Dispose()
	})
}

// Show marks the screen as dirty and renders/pushes the HTML. Rendering is
// skipped while no browser is connected, and coalesced according to
// [Opts.RefreshInterval].
func (s *Screen) Show() {
	s.SimulationScreen.Show()

	s.mu.Lock()
	s.dirty = true
	s.gen++
	first := !s.primed
	s.primed = true
	s.mu.Unlock()

	// Snapshot the completed frame for the async page renderer. Rendering from
	// the live simulation screen would tear while the app draws the next frame.
	s.renderNow()

	// Prime the first frame, so clients connecting later get a full page even
	// before their first resize event.
	if first {
		s.refreshNow()
		return
	}

	// Lazy: pushing is only useful while at least one browser is connected.
	// A client forces a refresh via its initial resize event.
	if s.page.Clients() == 0 {
		return
	}

	s.scheduleRefresh()
}

// Sync renders and pushes the current state immediately.
func (s *Screen) Sync() {
	s.SimulationScreen.Show()

	s.mu.Lock()
	s.dirty = true
	s.gen++
	s.mu.Unlock()

	s.renderNow()
	s.refreshNow()
}

// renderNow snapshots the completed grid into HTML on the drawing goroutine.
// The page renderer reads this snapshot, so it never sees a partially drawn
// frame (fast input used to flicker because of that).
func (s *Screen) renderNow() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.html = s.renderGrid()
	s.dirty = false
}

// scheduleRefresh coalesces refreshes according to [Opts.RefreshInterval],
// using a trailing refresh.
func (s *Screen) scheduleRefresh() {
	s.mu.Lock()
	if s.refreshTimer != nil {
		s.mu.Unlock()
		return
	}

	wait := time.Duration(0)
	if s.refreshInterval > 0 {
		wait = s.refreshInterval - time.Since(s.lastRefresh)
	}
	if wait <= 0 {
		s.mu.Unlock()
		s.refreshNow()
		return
	}

	s.refreshTimer = time.AfterFunc(wait, func() {
		s.mu.Lock()
		s.refreshTimer = nil
		s.mu.Unlock()
		s.refreshNow()
	})
	s.mu.Unlock()
}

// refreshNow renders the component and pushes the HTML on the screen's machine
// queue.
func (s *Screen) refreshNow() {
	if s.ctx.Err() != nil {
		return
	}

	s.mu.Lock()
	s.lastRefresh = time.Now()
	s.mu.Unlock()

	// Refresh serializes the component update on the screen's machine queue.
	// It is safe to call from any goroutine.
	s.page.Refresh(s.ctx)
}

// ResizeGrid resizes the grid, typically from the browser viewport. It posts a
// [tcell.EventResize] to the application and reports whether the size changed.
func (s *Screen) ResizeGrid(cols, rows int) bool {
	if cols <= 0 || rows <= 0 || cols > maxDim || rows > maxDim {
		return false
	}
	if w, h := s.Size(); w == cols && h == rows {
		return false
	}

	s.SimulationScreen.SetSize(cols, rows)
	_ = s.PostEvent(tcell.NewEventResize(cols, rows))

	s.mu.Lock()
	s.dirty = true
	s.gen++
	s.mu.Unlock()

	return true
}

// Handler serves the page, the livepage scripts and the WebSocket.
func (s *Screen) Handler() http.Handler {
	return s.page.NewHandler()
}

// Machine returns the machine used to serialize rendering and DOM events.
func (s *Screen) Machine() *am.Machine {
	return s.mach
}
