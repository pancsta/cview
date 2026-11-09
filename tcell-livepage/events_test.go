package tcelllivepage

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestMapKey(t *testing.T) {
	tests := []struct {
		name string
		in   domInput
		key  tcell.Key
		r    rune
		ok   bool
	}{
		{"rune", domInput{Key: "a"}, tcell.KeyRune, 'a', true},
		{"upper", domInput{Key: "Z"}, tcell.KeyRune, 'Z', true},
		{"unicode", domInput{Key: "ó"}, tcell.KeyRune, 'ó', true},
		{"space", domInput{Key: " "}, tcell.KeyRune, ' ', true},
		{"enter", domInput{Key: "Enter"}, tcell.KeyEnter, 0, true},
		{"tab", domInput{Key: "Tab"}, tcell.KeyTab, 0, true},
		{"backspace", domInput{Key: "Backspace"}, tcell.KeyBackspace, 0, true},
		{"escape", domInput{Key: "Escape"}, tcell.KeyEscape, 0, true},
		{"delete", domInput{Key: "Delete"}, tcell.KeyDelete, 0, true},
		{"insert", domInput{Key: "Insert"}, tcell.KeyInsert, 0, true},
		{"up", domInput{Key: "ArrowUp"}, tcell.KeyUp, 0, true},
		{"down", domInput{Key: "ArrowDown"}, tcell.KeyDown, 0, true},
		{"left", domInput{Key: "ArrowLeft"}, tcell.KeyLeft, 0, true},
		{"right", domInput{Key: "ArrowRight"}, tcell.KeyRight, 0, true},
		{"home", domInput{Key: "Home"}, tcell.KeyHome, 0, true},
		{"end", domInput{Key: "End"}, tcell.KeyEnd, 0, true},
		{"pgup", domInput{Key: "PageUp"}, tcell.KeyPgUp, 0, true},
		{"pgdn", domInput{Key: "PageDown"}, tcell.KeyPgDn, 0, true},
		{"f1", domInput{Key: "F1"}, tcell.KeyF1, 0, true},
		{"f12", domInput{Key: "F12"}, tcell.KeyF12, 0, true},
		{"modifier", domInput{Key: "Shift"}, 0, 0, false},
		{"empty", domInput{}, 0, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			key, r, ok := mapKey(&tc.in)
			if key != tc.key || r != tc.r || ok != tc.ok {
				t.Fatalf("got (%v, %q, %v), want (%v, %q, %v)",
					key, r, ok, tc.key, tc.r, tc.ok)
			}
		})
	}
}

func TestInjectKey(t *testing.T) {
	s := newTestScreen(t, 10, 5)

	s.injectKey(&domInput{Key: "a"})
	ev := pollKey(t, s)
	if ev.Key() != tcell.KeyRune || ev.Rune() != 'a' {
		t.Fatalf("unexpected event: key=%v rune=%q", ev.Key(), ev.Rune())
	}

	// Ctrl-C is normalized to the key code cview checks for quitting.
	s.injectKey(&domInput{Key: "c", Ctrl: true})
	ev = pollKey(t, s)
	if ev.Key() != tcell.KeyCtrlC {
		t.Fatalf("ctrl-c not normalized: %v", ev.Key())
	}
	if ev.Modifiers()&tcell.ModCtrl == 0 {
		t.Fatal("missing ctrl modifier")
	}

	// Shift-Tab is normalized to Backtab.
	s.injectKey(&domInput{Key: "Tab", Shift: true})
	ev = pollKey(t, s)
	if ev.Key() != tcell.KeyBacktab {
		t.Fatalf("shift-tab not normalized: %v", ev.Key())
	}
}

func pollKey(t *testing.T, s *Screen) *tcell.EventKey {
	t.Helper()
	ev := s.PollEvent()
	if ev == nil {
		t.Fatal("nil event")
	}
	key, ok := ev.(*tcell.EventKey)
	if !ok {
		t.Fatalf("unexpected event type %T", ev)
	}
	return key
}

func TestInjectMouse(t *testing.T) {
	s := newTestScreen(t, 10, 5)

	s.injectMouse(&domInput{Action: "down", X: 2, Y: 3, Buttons: 1})
	ev := pollMouse(t, s)
	if x, y := ev.Position(); x != 2 || y != 3 {
		t.Fatalf("unexpected position %d,%d", x, y)
	}
	if ev.Buttons() != tcell.Button1 {
		t.Fatalf("unexpected buttons %v", ev.Buttons())
	}

	s.injectMouse(&domInput{Action: "move", X: 4, Y: 1, Buttons: 1})
	ev = pollMouse(t, s)
	if ev.Buttons() != tcell.Button1 {
		t.Fatalf("unexpected move buttons %v", ev.Buttons())
	}

	s.injectMouse(&domInput{Action: "up", X: 4, Y: 1, Buttons: 0})
	ev = pollMouse(t, s)
	if ev.Buttons() != tcell.ButtonNone {
		t.Fatalf("expected release, got %v", ev.Buttons())
	}

	// right button
	s.injectMouse(&domInput{Action: "down", X: 0, Y: 0, Button: 2})
	ev = pollMouse(t, s)
	if ev.Buttons() != tcell.Button2 {
		t.Fatalf("right button not mapped: %v", ev.Buttons())
	}

	// clamp out-of-bounds positions
	s.injectMouse(&domInput{Action: "move", X: 999, Y: 999})
	ev = pollMouse(t, s)
	if x, y := ev.Position(); x != 9 || y != 4 {
		t.Fatalf("not clamped: %d,%d", x, y)
	}
}

func pollMouse(t *testing.T, s *Screen) *tcell.EventMouse {
	t.Helper()
	ev := s.PollEvent()
	if ev == nil {
		t.Fatal("nil event")
	}
	mouse, ok := ev.(*tcell.EventMouse)
	if !ok {
		t.Fatalf("unexpected event type %T", ev)
	}
	return mouse
}

func TestInjectWheel(t *testing.T) {
	s := newTestScreen(t, 10, 5)

	s.injectWheel(&domInput{X: 1, Y: 2, DeltaY: -120})
	ev := pollMouse(t, s)
	if ev.Buttons()&tcell.WheelUp == 0 {
		t.Fatalf("expected wheel up, got %v", ev.Buttons())
	}

	s.injectWheel(&domInput{X: 1, Y: 2, DeltaX: 120})
	ev = pollMouse(t, s)
	if ev.Buttons()&tcell.WheelRight == 0 {
		t.Fatalf("expected wheel right, got %v", ev.Buttons())
	}
}

func TestResize(t *testing.T) {
	s := newTestScreen(t, 10, 5)

	s.ResizeGrid(30, 12)
	if w, h := s.Size(); w != 30 || h != 12 {
		t.Fatalf("unexpected size %dx%d", w, h)
	}
	ev := s.PollEvent()
	resize, ok := ev.(*tcell.EventResize)
	if !ok {
		t.Fatalf("unexpected event type %T", ev)
	}
	if w, h := resize.Size(); w != 30 || h != 12 {
		t.Fatalf("unexpected resize %dx%d", w, h)
	}

	// no event for unchanged sizes
	s.ResizeGrid(30, 12)
	if w, h := s.Size(); w != 30 || h != 12 {
		t.Fatalf("unexpected size %dx%d", w, h)
	}
}
