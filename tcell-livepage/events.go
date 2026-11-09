package tcelllivepage

import (
	"encoding/json"

	"github.com/gdamore/tcell/v2"
	lp "github.com/pancsta/livepage"
)

const (
	typeKey    = "tcell-key"
	typeMouse  = "tcell-mouse"
	typeWheel  = "tcell-wheel"
	typeResize = "tcell-resize"
)

// domInput is the JSON envelope sent in [lp.DomEvent.Data].
type domInput struct {
	Key    string `json:"key"`
	Code   string `json:"code"`
	Action string `json:"action"`
	Repeat bool   `json:"repeat"`

	X       int     `json:"x"`
	Y       int     `json:"y"`
	Button  int     `json:"button"`
	Buttons int     `json:"buttons"`
	DeltaX  float64 `json:"deltaX"`
	DeltaY  float64 `json:"deltaY"`
	Cols    int     `json:"cols"`
	Rows    int     `json:"rows"`

	Ctrl  bool `json:"ctrl"`
	Alt   bool `json:"alt"`
	Shift bool `json:"shift"`
	Meta  bool `json:"meta"`
}

func (d *domInput) modMask() tcell.ModMask {
	var m tcell.ModMask
	if d.Shift {
		m |= tcell.ModShift
	}
	if d.Ctrl {
		m |= tcell.ModCtrl
	}
	if d.Alt {
		m |= tcell.ModAlt
	}
	if d.Meta {
		m |= tcell.ModMeta
	}
	return m
}

// handleEvent routes a livepage DOM event to the screen.
func (s *Screen) handleEvent(ev *lp.DomEvent) {
	if ev == nil {
		return
	}
	var in domInput
	if ev.Data != "" {
		if err := json.Unmarshal([]byte(ev.Data), &in); err != nil {
			return
		}
	}

	switch ev.Type {
	case typeKey:
		s.injectKey(&in)
	case typeMouse:
		s.injectMouse(&in)
	case typeWheel:
		s.injectWheel(&in)
	case typeResize:
		// Multiple browser clients share one grid: use the smallest viewport.
		if ev.Client != "" {
			s.resizeClient(ev.Client, in.Cols, in.Rows)
			return
		}
		if !s.ResizeGrid(in.Cols, in.Rows) {
			// A client connected with the same viewport size. Rendering may
			// have been skipped while offline, so force a fresh frame for the
			// dispatch which follows this event.
			s.forceFrame()
		}
	}
}

// resizeClient stores the viewport of a browser client and resizes the grid to
// the smallest viewport among all clients.
func (s *Screen) resizeClient(id string, cols, rows int) {
	if cols <= 0 || rows <= 0 || cols > maxDim || rows > maxDim {
		return
	}

	s.sizesMx.Lock()
	s.sizes[id] = [2]int{cols, rows}
	cols, rows, _ = minSize(s.sizes)
	s.sizesMx.Unlock()

	if !s.ResizeGrid(cols, rows) {
		// The smallest viewport did not change, but the new client needs a
		// frame for the dispatch which follows its resize event.
		s.forceFrame()
	}
}

// handleClient drops a disconnected client's viewport and resizes the grid to
// the smallest of the remaining clients.
func (s *Screen) handleClient(id string, connected bool) {
	if connected {
		return
	}

	s.sizesMx.Lock()
	delete(s.sizes, id)
	cols, rows, ok := minSize(s.sizes)
	s.sizesMx.Unlock()
	if !ok {
		return
	}

	if !s.ResizeGrid(cols, rows) {
		s.forceFrame()
	}
}

// minSize returns the smallest viewport among all clients.
func minSize(sizes map[string][2]int) (int, int, bool) {
	var (
		cols, rows int
		found      bool
	)
	for _, size := range sizes {
		if !found {
			cols, rows, found = size[0], size[1], true
			continue
		}
		cols = min(cols, size[0])
		rows = min(rows, size[1])
	}

	return cols, rows, found
}

// forceFrame renders a fresh frame for the next livepage dispatch, even when
// the grid did not change.
func (s *Screen) forceFrame() {
	s.mu.Lock()
	s.dirty = true
	s.gen++
	s.mu.Unlock()
	s.renderNow()
}

func (s *Screen) injectKey(in *domInput) {
	key, r, ok := mapKey(in)
	if !ok {
		return
	}
	// NewEventKey normalizes Ctrl+letter and Shift+Tab.
	_ = s.PostEvent(tcell.NewEventKey(key, r, in.modMask()))
}

func (s *Screen) injectMouse(in *domInput) {
	var mask tcell.ButtonMask
	switch in.Action {
	case "down", "up", "move":
		mask = browserButtons(in.Buttons)
		if in.Action == "down" && mask == 0 {
			mask = browserButton(in.Button)
		}
	default:
		return
	}

	x, y := s.clampCell(in.X, in.Y)
	_ = s.PostEvent(tcell.NewEventMouse(x, y, mask, in.modMask()))
}

func (s *Screen) injectWheel(in *domInput) {
	var mask tcell.ButtonMask
	if in.DeltaY < 0 {
		mask |= tcell.WheelUp
	} else if in.DeltaY > 0 {
		mask |= tcell.WheelDown
	}
	if in.DeltaX < 0 {
		mask |= tcell.WheelLeft
	} else if in.DeltaX > 0 {
		mask |= tcell.WheelRight
	}
	if mask == 0 {
		return
	}

	x, y := s.clampCell(in.X, in.Y)
	_ = s.PostEvent(tcell.NewEventMouse(x, y, mask, in.modMask()))
}

func (s *Screen) clampCell(x, y int) (int, int) {
	w, h := s.Size()
	if w <= 0 || h <= 0 {
		return 0, 0
	}
	if x < 0 {
		x = 0
	} else if x >= w {
		x = w - 1
	}
	if y < 0 {
		y = 0
	} else if y >= h {
		y = h - 1
	}
	return x, y
}

// browserButton converts a DOM MouseEvent.button value to a tcell button.
func browserButton(button int) tcell.ButtonMask {
	switch button {
	case 0:
		return tcell.Button1 // left
	case 1:
		return tcell.Button3 // middle
	case 2:
		return tcell.Button2 // right
	}
	return tcell.ButtonNone
}

// browserButtons converts a DOM MouseEvent.buttons bitmask to a tcell mask.
func browserButtons(buttons int) tcell.ButtonMask {
	var mask tcell.ButtonMask
	if buttons&1 != 0 {
		mask |= tcell.Button1 // left
	}
	if buttons&2 != 0 {
		mask |= tcell.Button2 // right
	}
	if buttons&4 != 0 {
		mask |= tcell.Button3 // middle
	}
	return mask
}

var namedKeys = map[string]tcell.Key{
	"Enter":      tcell.KeyEnter,
	"Tab":        tcell.KeyTab,
	"Backspace":  tcell.KeyBackspace,
	"Delete":     tcell.KeyDelete,
	"Insert":     tcell.KeyInsert,
	"Escape":     tcell.KeyEscape,
	"Esc":        tcell.KeyEscape,
	"ArrowUp":    tcell.KeyUp,
	"ArrowDown":  tcell.KeyDown,
	"ArrowLeft":  tcell.KeyLeft,
	"ArrowRight": tcell.KeyRight,
	"Home":       tcell.KeyHome,
	"End":        tcell.KeyEnd,
	"PageUp":     tcell.KeyPgUp,
	"PageDown":   tcell.KeyPgDn,
	"F1":         tcell.KeyF1,
	"F2":         tcell.KeyF2,
	"F3":         tcell.KeyF3,
	"F4":         tcell.KeyF4,
	"F5":         tcell.KeyF5,
	"F6":         tcell.KeyF6,
	"F7":         tcell.KeyF7,
	"F8":         tcell.KeyF8,
	"F9":         tcell.KeyF9,
	"F10":        tcell.KeyF10,
	"F11":        tcell.KeyF11,
	"F12":        tcell.KeyF12,
}

// mapKey converts a DOM key to a tcell key and rune.
func mapKey(in *domInput) (tcell.Key, rune, bool) {
	if key, ok := namedKeys[in.Key]; ok {
		return key, 0, true
	}
	if in.Key == " " || in.Key == "Spacebar" {
		return tcell.KeyRune, ' ', true
	}
	if r := []rune(in.Key); len(r) == 1 {
		return tcell.KeyRune, r[0], true
	}
	return 0, 0, false
}
