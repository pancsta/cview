package tcelllivepage

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func newTestScreen(t *testing.T, w, h int) *Screen {
	t.Helper()

	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sim.Fini)
	sim.SetSize(w, h)
	sim.Clear()

	s := &Screen{
		SimulationScreen: sim,
		theme:            DefaultTheme(),
		fontSize:         DefaultFontSize,
		fontFamily:       "monospace",
		dirty:            true,
		styles:           map[tcell.Style]string{},
	}
	return s
}

func TestGridDefaultStyle(t *testing.T) {
	s := newTestScreen(t, 4, 2)
	s.SetContent(0, 0, 'A', nil, tcell.StyleDefault)
	s.SetContent(1, 0, 'B', nil, tcell.StyleDefault)
	s.SetContent(2, 0, 'C', nil, tcell.StyleDefault)
	s.SetContent(3, 0, 'D', nil, tcell.StyleDefault)

	html := s.gridHTML()
	if !strings.Contains(html, `class="tcell-grid"`) {
		t.Fatalf("missing grid: %s", html)
	}
	if !strings.Contains(html, `class="tcell-probe"`) {
		t.Fatalf("missing probe: %s", html)
	}
	// Default styling is inherited, no span needed.
	if !strings.Contains(html, `<div class="tcell-row">ABCD</div>`) {
		t.Fatalf("unexpected row: %s", html)
	}
}

func TestGridStyles(t *testing.T) {
	s := newTestScreen(t, 8, 1)
	s.SetContent(0, 0, 'r', nil, tcell.StyleDefault.Foreground(tcell.ColorRed).Bold(true))
	s.SetContent(1, 0, 'g', nil, tcell.StyleDefault.Background(tcell.ColorGreen))
	s.SetContent(2, 0, 'i', nil, tcell.StyleDefault.Italic(true).Underline(true))
	s.SetContent(3, 0, 'x', nil, tcell.StyleDefault.Reverse(true).Foreground(tcell.ColorBlue).Background(tcell.ColorYellow))
	s.SetContent(4, 0, 'd', nil, tcell.StyleDefault.Dim(true))
	s.SetContent(5, 0, 's', nil, tcell.StyleDefault.StrikeThrough(true))

	html := s.gridHTML()
	for _, want := range []string{
		"color:#FF0000;font-weight:700;",
		"background-color:#008000;",
		"text-decoration:underline;font-style:italic;",
		// reverse swaps fg and bg
		"color:#FFFF00;background-color:#0000FF;",
		"opacity:.7;",
		"text-decoration:line-through;",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %q in %s", want, html)
		}
	}
}

func TestGridEscaping(t *testing.T) {
	s := newTestScreen(t, 4, 1)
	s.SetContent(0, 0, '<', nil, tcell.StyleDefault)
	s.SetContent(1, 0, '&', nil, tcell.StyleDefault)
	s.SetContent(2, 0, '>', nil, tcell.StyleDefault)

	html := s.gridHTML()
	if !strings.Contains(html, "&lt;&amp;&gt;") {
		t.Fatalf("not escaped: %s", html)
	}
}

func TestGridWideRunes(t *testing.T) {
	s := newTestScreen(t, 4, 1)
	s.SetContent(0, 0, '世', nil, tcell.StyleDefault)
	s.SetContent(2, 0, 'X', nil, tcell.StyleDefault)

	html := s.gridHTML()
	if !strings.Contains(html, `class="tc-wide" style="width:2ch;"`) {
		t.Fatalf("wide rune not wrapped: %s", html)
	}
	if !strings.Contains(html, "世</span>X") {
		t.Fatalf("wide rune not followed by X: %s", html)
	}
	if strings.Contains(html, "世世") {
		t.Fatalf("wide rune duplicated: %s", html)
	}
}

func TestGridCursor(t *testing.T) {
	s := newTestScreen(t, 4, 1)
	s.SetContent(0, 0, 'a', nil, tcell.StyleDefault)
	s.SetContent(1, 0, 'b', nil, tcell.StyleDefault)
	s.ShowCursor(1, 0)

	html := s.gridHTML()
	if !strings.Contains(html, `<span class="tc-cursor">b</span>`) {
		t.Fatalf("cursor not rendered: %s", html)
	}
}
