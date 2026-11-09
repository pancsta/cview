package tcelllivepage

// ScreenMux is a tcell screen which multiplexes a single core simulation
// screen to multiple UIs. An app draws into the core, which is projected to all
// attached screens (a PTY, SSH sessions, a browser, ...). Events from all UIs
// are forwarded into a single queue.
//
// The core is resized to the smallest attached UI, so all UIs share the same
// grid. With no UIs attached, the core keeps its default size.
//
// Projection runs in a per-UI goroutine, so a slow or dead UI never blocks the
// app's draw loop.

import (
	"context"
	"math/bits"
	"slices"
	"sync"

	"github.com/gdamore/tcell/v2"
)

// AttachOpts configures a single UI projection of a [ScreenMux].
type AttachOpts struct {
	// DetachOnCtrlQ detaches only this UI on Ctrl+Q instead of forwarding the
	// event to the app.
	DetachOnCtrlQ bool
	// DeferredSize excludes the UI from size negotiation until it reports its
	// first resize. Used for browsers, before a client connects.
	DeferredSize bool
	// Clients reports the number of connected clients of this UI, if any.
	// A deferred UI with no clients is kept at the core size.
	Clients func() int
}

// muxCell is a single projected cell, snapshotted at Show time.
type muxCell struct {
	x, y  int
	mainc rune
	combc []rune
	style tcell.Style
}

// MuxClient is a single UI projection of the core screen.
type MuxClient struct {
	screen tcell.Screen
	close  func()
	opts   AttachOpts
	// pending holds cells to project on the next kick, latest wins.
	pending map[int]muxCell
	// marginW, marginH are the core dims as of the last margin clear.
	marginW, marginH int
	// resync makes the next projection render even without clients.
	resync bool
	kick   chan struct{}
	done   chan struct{}
	// projectDone is closed when the projector goroutine exits.
	projectDone chan struct{}
	doneOne     sync.Once
}

// Done is closed when the UI gets detached.
func (c *MuxClient) Done() <-chan struct{} {
	return c.done
}

// ScreenMux multiplexes one core simulation screen to multiple UIs. It
// implements [tcell.Screen] and can be passed to cview's
// Application.SetScreen.
type ScreenMux struct {
	tcell.SimulationScreen

	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	clients []*MuxClient
	// dirty is a bitset of cells changed since the last Show.
	dirty []uint64
	w, h  int

	// UI state, replayed to new clients
	style tcell.Style
	mouse bool
	paste bool

	events   chan tcell.Event
	finiOnce sync.Once
}

var _ tcell.Screen = (*ScreenMux)(nil)

// NewScreenMux creates an initialized multiplexer with a [DefaultCols] x
// [DefaultRows] core screen. It lives as long as ctx.
func NewScreenMux(ctx context.Context) (*ScreenMux, error) {
	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		return nil, err
	}
	sim.SetSize(DefaultCols, DefaultRows)
	sim.Clear()
	sim.Show()

	ctx, cancel := context.WithCancel(ctx)
	m := &ScreenMux{
		SimulationScreen: sim,
		ctx:              ctx,
		cancel:           cancel,
		events:           make(chan tcell.Event, 64),
		paste:            true,
	}
	m.w, m.h = sim.Size()
	m.dirty = make([]uint64, (m.w*m.h+63)/64)

	return m, nil
}

// Attach registers a UI screen and starts forwarding its events and cells.
func (m *ScreenMux) Attach(
	screen tcell.Screen, closeFn func(), opts AttachOpts,
) *MuxClient {
	c := &MuxClient{
		screen:      screen,
		close:       closeFn,
		opts:        opts,
		pending:     make(map[int]muxCell),
		kick:        make(chan struct{}, 1),
		done:        make(chan struct{}),
		projectDone: make(chan struct{}),
	}

	m.mu.Lock()
	m.clients = append(m.clients, c)
	style, mouse, paste := m.style, m.mouse, m.paste
	m.mu.Unlock()

	screen.SetStyle(style)
	if mouse {
		screen.EnableMouse()
	}
	if paste {
		screen.EnablePaste()
	}

	go m.project(c)
	go m.pollClient(c)

	// size negotiation may not change the core, but a new UI needs a full
	// redraw anyway
	m.recalcSize()
	w, h := m.Size()
	_ = m.PostEvent(tcell.NewEventResize(w, h))
	// the new UI may be larger than the core
	m.invalidateMargins(c)

	return c
}

// Detach removes a UI and releases its screen.
func (m *ScreenMux) Detach(c *MuxClient) {
	m.mu.Lock()
	idx := slices.Index(m.clients, c)
	if idx < 0 {
		m.mu.Unlock()
		return
	}
	m.clients = slices.Delete(m.clients, idx, idx+1)
	m.mu.Unlock()

	c.finish()
	m.recalcSize()
}

// SetDeferred excludes a UI from size negotiation and keeps its grid at the
// core size. Used when the last client of a deferred UI disconnects.
func (m *ScreenMux) SetDeferred(c *MuxClient, deferred bool) {
	m.mu.Lock()
	c.opts.DeferredSize = deferred
	resized := deferred && m.syncDeferredLocked(c, m.w, m.h)
	m.mu.Unlock()

	m.recalcSize()
	if resized {
		m.kick(c)
	}
}

// invalidateMargins makes the projector clear the area outside the core, eg
// after the core shrank or the UI grew.
func (m *ScreenMux) invalidateMargins(c *MuxClient) {
	m.mu.Lock()
	c.marginW, c.marginH = -1, -1
	m.mu.Unlock()

	m.kick(c)
}

// syncDeferredLocked sizes a deferred UI without clients to the core. Must be
// called with the lock held.
func (m *ScreenMux) syncDeferredLocked(c *MuxClient, w, h int) bool {
	if !c.opts.DeferredSize {
		return false
	}
	if c.opts.Clients != nil && c.opts.Clients() > 0 {
		return false
	}
	sim, ok := c.screen.(tcell.SimulationScreen)
	if !ok {
		return false
	}
	if cw, ch := sim.Size(); cw == w && ch == h {
		return false
	}
	// SetSize is silent, it does not post a resize event
	sim.SetSize(w, h)
	// render the new grid even without clients
	c.resync = true
	return true
}

func (m *ScreenMux) kick(c *MuxClient) {
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

func (c *MuxClient) finish() {
	c.doneOne.Do(func() {
		close(c.done)
		// Fini may block (eg on SSH), same as cview does
		go func() {
			defer func() { _ = recover() }()
			if c.close != nil {
				c.close()
			}
			// the projector must not write to a finalized screen
			<-c.projectDone
			c.screen.Fini()
		}()
	})
}

// pollClient forwards the events of a single UI until it disconnects.
func (m *ScreenMux) pollClient(c *MuxClient) {
	for {
		ev := c.screen.PollEvent()
		if ev == nil {
			break
		}

		switch ev := ev.(type) {
		case *tcell.EventResize:
			if c.opts.DeferredSize {
				m.mu.Lock()
				c.opts.DeferredSize = false
				m.mu.Unlock()
			}
			// the core size is recomputed from all UIs, so drop the raw event
			m.recalcSize()
			// this UI may have grown beyond the core
			m.invalidateMargins(c)
			continue

		case *tcell.EventError:
			// eg SSH disconnect
			m.Detach(c)
			return

		case *tcell.EventKey:
			if c.opts.DetachOnCtrlQ && ev.Key() == tcell.KeyCtrlQ {
				m.Detach(c)
				return
			}
		}

		if err := m.post(ev); err != nil {
			return
		}
	}

	m.Detach(c)
}

// project writes the pending cells to a single UI.
func (m *ScreenMux) project(c *MuxClient) {
	defer func() { _ = recover() }()
	defer close(c.projectDone)

	for {
		select {
		case <-c.kick:
		case <-c.done:
			return
		}

		m.mu.Lock()
		pending := c.pending
		c.pending = make(map[int]muxCell, len(pending))
		w, h := m.w, m.h
		marginW, marginH := c.marginW, c.marginH
		c.marginW, c.marginH = w, h
		resync := c.resync
		// keep resync until a frame with content is projected
		if resync && len(pending) > 0 {
			c.resync = false
		}
		m.mu.Unlock()

		cw, ch := c.screen.Size()
		// clear the area outside the core, eg after shrinking
		clearMargins := (marginW != w || marginH != h) && (w < cw || h < ch)
		if clearMargins {
			clearClientMargins(c.screen, cw, ch, w, h)
		}
		if len(pending) == 0 && !clearMargins {
			if resync {
				// eg a deferred screen resized with no clients, render it
				c.screen.Sync()
			}
			continue
		}

		for _, cell := range pending {
			if cell.x < cw && cell.y < ch {
				c.screen.SetContent(cell.x, cell.y, cell.mainc, cell.combc,
					cell.style)
			}
		}
		if resync {
			c.screen.Sync()
		} else {
			c.screen.Show()
		}
	}
}

// clearClientMargins fills the part of the UI outside the core with the
// default background.
func clearClientMargins(s tcell.Screen, cw, ch, w, h int) {
	for y := 0; y < ch; y++ {
		start := 0
		if y < h {
			start = min(w, cw)
		}
		for x := start; x < cw; x++ {
			s.SetContent(x, y, ' ', nil, tcell.StyleDefault)
		}
	}
}

// recalcSize resizes the core to the smallest UI and notifies the app.
func (m *ScreenMux) recalcSize() {
	m.mu.Lock()
	w, h := 0, 0
	for _, c := range m.clients {
		if c.opts.DeferredSize {
			continue
		}
		cw, ch := c.screen.Size()
		if cw <= 0 || ch <= 0 {
			continue
		}
		if w == 0 || cw < w {
			w = cw
		}
		if h == 0 || ch < h {
			h = ch
		}
	}
	if w == 0 || h == 0 {
		w, h = DefaultCols, DefaultRows
	}

	changed := w != m.w || h != m.h
	var clients []*MuxClient
	if changed {
		m.w, m.h = w, h
		m.dirty = make([]uint64, (w*h+63)/64)
		for i := range m.dirty {
			m.dirty[i] = ^uint64(0)
		}
		clients = slices.Clone(m.clients)
		for _, c := range clients {
			c.pending = make(map[int]muxCell)
			m.syncDeferredLocked(c, w, h)
		}
		m.SimulationScreen.SetSize(w, h)
	}
	m.mu.Unlock()

	if changed {
		for _, c := range clients {
			m.kick(c)
		}
		_ = m.PostEvent(tcell.NewEventResize(w, h))
	}
}

func (m *ScreenMux) markAll() {
	m.mu.Lock()
	for i := range m.dirty {
		m.dirty[i] = ^uint64(0)
	}
	m.mu.Unlock()
}

// ///// ///// /////

// ///// TCEL SCREEN

// ///// ///// /////

// Init is a no-op, the screen is initialized by [NewScreenMux].
func (m *ScreenMux) Init() error {
	return nil
}

// Fini detaches all UIs and the core screen.
func (m *ScreenMux) Fini() {
	m.finiOnce.Do(func() {
		m.mu.Lock()
		clients := m.clients
		m.clients = nil
		m.mu.Unlock()

		m.cancel()
		for _, c := range clients {
			c.finish()
		}
		m.SimulationScreen.Fini()
	})
}

// SetContent writes a cell to the core and marks it for projection.
func (m *ScreenMux) SetContent(
	x, y int, mainc rune, combc []rune, style tcell.Style,
) {
	m.SimulationScreen.SetContent(x, y, mainc, combc, style)

	m.mu.Lock()
	if x >= 0 && y >= 0 && x < m.w && y < m.h {
		i := y*m.w + x
		m.dirty[i/64] |= 1 << uint(i%64)
	}
	m.mu.Unlock()
}

func (m *ScreenMux) Clear() {
	m.SimulationScreen.Clear()
	m.markAll()
}

func (m *ScreenMux) Fill(r rune, style tcell.Style) {
	m.SimulationScreen.Fill(r, style)
	m.markAll()
}

// Sync projects the current grid immediately.
func (m *ScreenMux) Sync() {
	m.markAll()
	m.Show()
}

// Show snapshots the changed cells and queues them for projection. The
// snapshot is taken on the app goroutine, right after the frame has been
// drawn, so UIs never see a partially drawn frame.
func (m *ScreenMux) Show() {
	m.SimulationScreen.Show()

	m.mu.Lock()
	clients := slices.Clone(m.clients)
	if len(clients) == 0 {
		m.clearDirty()
		m.mu.Unlock()
		return
	}

	// GetContents is safe here: SetSize also runs under m.mu
	cells, w, h := m.SimulationScreen.GetContents()
	frame := make([]muxCell, 0, 256)
	for i, word := range m.dirty {
		for word != 0 {
			b := bits.TrailingZeros64(word)
			word &= word - 1
			idx := i*64 + b
			if idx >= w*h || idx >= len(cells) {
				continue
			}
			cell := &cells[idx]
			c := muxCell{
				x:     idx % w,
				y:     idx / w,
				mainc: ' ',
				style: cell.Style,
			}
			if len(cell.Runes) > 0 {
				c.mainc = cell.Runes[0]
				c.combc = cell.Runes[1:]
			}
			frame = append(frame, c)
		}
	}
	m.clearDirty()
	for _, c := range clients {
		for _, cell := range frame {
			c.pending[cell.y*w+cell.x] = cell
		}
	}
	m.mu.Unlock()

	if len(frame) == 0 {
		return
	}
	for _, c := range clients {
		m.kick(c)
	}
}

// clearDirty must be called with the lock held.
func (m *ScreenMux) clearDirty() {
	for i := range m.dirty {
		m.dirty[i] = 0
	}
}

// PollEvent returns the next event from any UI.
func (m *ScreenMux) PollEvent() tcell.Event {
	select {
	case ev := <-m.events:
		return ev
	case <-m.ctx.Done():
		return nil
	}
}

// PostEvent queues an event for the app, eg an injected keystroke.
func (m *ScreenMux) PostEvent(ev tcell.Event) error {
	return m.post(ev)
}

func (m *ScreenMux) post(ev tcell.Event) error {
	select {
	case m.events <- ev:
		return nil
	case <-m.ctx.Done():
		return context.Canceled
	}
}

func (m *ScreenMux) SetStyle(style tcell.Style) {
	m.mu.Lock()
	m.style = style
	clients := slices.Clone(m.clients)
	m.mu.Unlock()

	m.SimulationScreen.SetStyle(style)
	for _, c := range clients {
		c.screen.SetStyle(style)
	}
}

func (m *ScreenMux) ShowCursor(x, y int) {
	m.mu.Lock()
	clients := slices.Clone(m.clients)
	m.mu.Unlock()

	m.SimulationScreen.ShowCursor(x, y)
	for _, c := range clients {
		c.screen.ShowCursor(x, y)
	}
}

func (m *ScreenMux) EnableMouse(flags ...tcell.MouseFlags) {
	m.mu.Lock()
	m.mouse = true
	clients := slices.Clone(m.clients)
	m.mu.Unlock()

	m.SimulationScreen.EnableMouse(flags...)
	for _, c := range clients {
		c.screen.EnableMouse(flags...)
	}
}

func (m *ScreenMux) DisableMouse() {
	m.mu.Lock()
	m.mouse = false
	clients := slices.Clone(m.clients)
	m.mu.Unlock()

	m.SimulationScreen.DisableMouse()
	for _, c := range clients {
		c.screen.DisableMouse()
	}
}

func (m *ScreenMux) EnablePaste() {
	m.mu.Lock()
	m.paste = true
	clients := slices.Clone(m.clients)
	m.mu.Unlock()

	m.SimulationScreen.EnablePaste()
	for _, c := range clients {
		c.screen.EnablePaste()
	}
}

func (m *ScreenMux) DisablePaste() {
	m.mu.Lock()
	m.paste = false
	clients := slices.Clone(m.clients)
	m.mu.Unlock()

	m.SimulationScreen.DisablePaste()
	for _, c := range clients {
		c.screen.DisablePaste()
	}
}
