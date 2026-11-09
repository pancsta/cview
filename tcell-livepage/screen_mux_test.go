package tcelllivepage

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func TestScreenMuxSize(t *testing.T) {
	m, err := NewScreenMux(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Fini)

	// no UIs
	w, h := m.Size()
	if w != DefaultCols || h != DefaultRows {
		t.Fatalf("default size %dx%d", w, h)
	}

	// the smallest UI wins
	a := newMuxScreen(t, 100, 50)
	ca := m.Attach(a, nil, AttachOpts{})
	if w, h = m.Size(); w != 100 || h != 50 {
		t.Fatalf("size %dx%d", w, h)
	}

	b := newMuxScreen(t, 80, 24)
	cb := m.Attach(b, nil, AttachOpts{})
	if w, h = m.Size(); w != 80 || h != 24 {
		t.Fatalf("size %dx%d", w, h)
	}

	// detaching grows the core back and notifies the app
	muxDrain(m)
	m.Detach(cb)
	if w, h = m.Size(); w != 100 || h != 50 {
		t.Fatalf("size %dx%d", w, h)
	}
	if _, ok := muxEvent(t, m).(*tcell.EventResize); !ok {
		t.Fatal("expected a resize event")
	}

	// deferred UI does not shrink the core before its first resize
	d := newMuxScreen(t, 10, 5)
	cd := m.Attach(d, nil, AttachOpts{DeferredSize: true})
	if w, h = m.Size(); w != 100 || h != 50 {
		t.Fatalf("size %dx%d", w, h)
	}
	if err := d.PostEvent(tcell.NewEventResize(10, 5)); err != nil {
		t.Fatal(err)
	}
	muxDrain(m)
	muxWait(t, func() bool {
		w, h := m.Size()
		return w == 10 && h == 5
	})

	m.Detach(cd)
	m.Detach(ca)
}

func TestScreenMuxProject(t *testing.T) {
	m, err := NewScreenMux(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Fini)

	a := newMuxScreen(t, 20, 5)
	m.Attach(a, nil, AttachOpts{})

	// dirty cells get projected on Show
	m.SetContent(1, 1, 'X', nil, tcell.StyleDefault)
	m.Show()
	muxWait(t, func() bool {
		str, _, _ := a.Get(1, 1)
		return str == "X"
	})
}

func TestScreenMuxEvents(t *testing.T) {
	m, err := NewScreenMux(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Fini)

	a := newMuxScreen(t, 20, 5)
	ca := m.Attach(a, nil, AttachOpts{DetachOnCtrlQ: true})
	muxDrain(m)

	// keys are forwarded to the app
	a.InjectKey(tcell.KeyRune, 'x', tcell.ModNone)
	ev, ok := muxEvent(t, m).(*tcell.EventKey)
	if !ok || ev.Rune() != 'x' {
		t.Fatalf("event %v", ev)
	}

	// ctrl+q detaches only this UI
	a.InjectKey(tcell.KeyCtrlQ, 0, tcell.ModNone)
	muxWait(t, func() bool {
		select {
		case <-ca.Done():
			return true
		default:
			return false
		}
	})

	// errors detach the UI too
	b := newMuxScreen(t, 20, 5)
	cb := m.Attach(b, nil, AttachOpts{})
	if err := b.PostEvent(tcell.NewEventError(errors.New("eof"))); err != nil {
		t.Fatal(err)
	}
	muxWait(t, func() bool {
		select {
		case <-cb.Done():
			return true
		default:
			return false
		}
	})
}

func TestScreenMuxDeferred(t *testing.T) {
	m, err := NewScreenMux(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Fini)

	// a PTY-like UI
	pty := newMuxScreen(t, 100, 30)
	m.Attach(pty, nil, AttachOpts{})

	// a deferred browser-like UI, no clients yet
	var clients atomic.Int32
	browser := newMuxScreen(t, 100, 30)
	live := m.Attach(browser, nil, AttachOpts{
		DeferredSize: true,
		Clients:      func() int { return int(clients.Load()) },
	})

	if w, h := m.Size(); w != 100 || h != 30 {
		t.Fatalf("size %dx%d", w, h)
	}

	// the PTY shrinks, the deferred UI follows the core
	pty.SetSize(80, 24)
	if err := pty.PostEvent(tcell.NewEventResize(80, 24)); err != nil {
		t.Fatal(err)
	}
	muxWait(t, func() bool {
		w, h := m.Size()
		return w == 80 && h == 24
	})
	muxWait(t, func() bool {
		w, h := browser.Size()
		return w == 80 && h == 24
	})

	// a client connects and reports a smaller viewport
	clients.Store(1)
	browser.SetSize(60, 20)
	if err := browser.PostEvent(tcell.NewEventResize(60, 20)); err != nil {
		t.Fatal(err)
	}
	muxWait(t, func() bool {
		w, h := m.Size()
		return w == 60 && h == 20
	})

	// the last client disconnects, the UI follows the PTY again
	clients.Store(0)
	m.SetDeferred(live, true)
	muxWait(t, func() bool {
		w, h := m.Size()
		return w == 80 && h == 24
	})
	muxWait(t, func() bool {
		w, h := browser.Size()
		return w == 80 && h == 24
	})
}

func TestScreenMuxMargins(t *testing.T) {
	m, err := NewScreenMux(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Fini)

	// the core is 10x3
	small := newMuxScreen(t, 10, 3)
	m.Attach(small, nil, AttachOpts{})

	// a larger UI gets its area outside the core cleared
	big := newMuxScreen(t, 20, 5)
	big.SetContent(15, 4, 'Z', nil, tcell.StyleDefault)
	m.Attach(big, nil, AttachOpts{})

	muxWait(t, func() bool {
		str, style, _ := big.Get(15, 4)
		return str == " " && style == tcell.StyleDefault
	})

	// in-core cells are projected as usual
	m.SetContent(0, 0, 'X', nil, tcell.StyleDefault)
	m.Show()
	muxWait(t, func() bool {
		str, _, _ := big.Get(0, 0)
		return str == "X"
	})

	// shrinking the core clears the new margins of all UIs
	tiny := newMuxScreen(t, 5, 2)
	big.SetContent(7, 3, 'Z', nil, tcell.StyleDefault)
	m.Attach(tiny, nil, AttachOpts{})
	m.Show()
	muxWait(t, func() bool {
		w, h := m.Size()
		if w != 5 || h != 2 {
			return false
		}
		str, style, _ := big.Get(7, 3)
		return str == " " && style == tcell.StyleDefault
	})
}

func TestScreenMuxSnapshot(t *testing.T) {
	m, err := NewScreenMux(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Fini)

	gate := &gateScreen{
		SimulationScreen: newMuxScreen(t, 20, 5),
		entered:          make(chan struct{}),
		release:          make(chan struct{}),
	}
	m.Attach(gate, nil, AttachOpts{})

	// Show X and keep the projector blocked mid-write
	m.SetContent(1, 1, 'X', nil, tcell.StyleDefault)
	m.Show()
	select {
	case <-gate.entered:
	case <-time.After(time.Second):
		t.Fatal("projection did not start")
	}

	// mutate the core after the snapshot, without another Show
	m.SetContent(1, 1, 'Y', nil, tcell.StyleDefault)
	close(gate.release)

	// the projected cell is the snapshotted one
	muxWait(t, func() bool {
		str, _, _ := gate.Get(1, 1)
		return str == "X"
	})

	// the next Show projects the new content
	m.Show()
	muxWait(t, func() bool {
		str, _, _ := gate.Get(1, 1)
		return str == "Y"
	})
}

// gateScreen blocks the first projection write, to test frame snapshots.
type gateScreen struct {
	tcell.SimulationScreen
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *gateScreen) SetContent(
	x, y int, mainc rune, combc []rune, style tcell.Style,
) {
	s.once.Do(func() { close(s.entered) })
	<-s.release
	s.SimulationScreen.SetContent(x, y, mainc, combc, style)
}

func newMuxScreen(t *testing.T, w, h int) tcell.SimulationScreen {
	t.Helper()

	s := tcell.NewSimulationScreen("UTF-8")
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	s.SetSize(w, h)
	return s
}

func muxWait(t *testing.T, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met")
}

func muxDrain(m *ScreenMux) {
	for {
		select {
		case <-m.events:
		default:
			return
		}
	}
}

func muxEvent(t *testing.T, m *ScreenMux) tcell.Event {
	t.Helper()

	select {
	case ev := <-m.events:
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("no mux event")
		return nil
	}
}
