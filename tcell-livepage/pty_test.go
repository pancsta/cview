//go:build !windows

package tcelllivepage

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/hinshun/vt10x"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodeKey(t *testing.T) {
	tests := []struct {
		name string
		ev   *tcell.EventKey
		want string
	}{
		{"rune", tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone), "a"},
		{"rune upper", tcell.NewEventKey(tcell.KeyRune, 'A', tcell.ModShift), "A"},
		{"enter", tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), "\r"},
		{"tab", tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone), "\t"},
		{"backspace", tcell.NewEventKey(tcell.KeyBackspace, 0, tcell.ModNone), "\x7f"},
		{"backspace2", tcell.NewEventKey(tcell.KeyBackspace2, 0, tcell.ModNone), "\x7f"},
		{"esc", tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone), "\x1b"},
		{"up", tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone), "\x1b[A"},
		{"down", tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), "\x1b[B"},
		{"right", tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone), "\x1b[C"},
		{"left", tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModNone), "\x1b[D"},
		{"home", tcell.NewEventKey(tcell.KeyHome, 0, tcell.ModNone), "\x1b[H"},
		{"end", tcell.NewEventKey(tcell.KeyEnd, 0, tcell.ModNone), "\x1b[F"},
		{"delete", tcell.NewEventKey(tcell.KeyDelete, 0, tcell.ModNone), "\x1b[3~"},
		{"pgup", tcell.NewEventKey(tcell.KeyPgUp, 0, tcell.ModNone), "\x1b[5~"},
		{"f1", tcell.NewEventKey(tcell.KeyF1, 0, tcell.ModNone), "\x1bOP"},
		{"ctrl c", tcell.NewEventKey(tcell.KeyCtrlC, 0, tcell.ModCtrl), "\x03"},
		{"ctrl d", tcell.NewEventKey(tcell.KeyCtrlD, 0, tcell.ModCtrl), "\x04"},
		{"ctrl a rune", tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModCtrl), "\x01"},
		{"alt rune", tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModAlt), "\x1bx"},
		{"alt up", tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModAlt), "\x1b\x1b[A"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, string(encodeKey(tt.ev)))
		})
	}
}

func TestVtColor(t *testing.T) {
	assert.Equal(t, tcell.ColorDefault, vtColor(vt10x.DefaultFG))
	assert.Equal(t, tcell.ColorDefault, vtColor(vt10x.DefaultBG))
	assert.Equal(t, tcell.PaletteColor(1), vtColor(vt10x.Red))
	assert.Equal(t, tcell.PaletteColor(200), vtColor(vt10x.Color(200)))
	assert.Equal(t, tcell.NewHexColor(0x102030),
		vtColor(vt10x.Color(0x102030)))
}

func TestVtStyle(t *testing.T) {
	style := vtStyle(vt10x.Glyph{
		FG:   vt10x.Green,
		BG:   vt10x.Black,
		Mode: vtAttrBold | vtAttrUnderline | vtAttrReverse,
	})

	fg, bg, attrs := style.Decompose()
	assert.Equal(t, tcell.PaletteColor(2), fg)
	assert.Equal(t, tcell.PaletteColor(0), bg)
	assert.True(t, attrs&tcell.AttrBold != 0)
	assert.True(t, attrs&tcell.AttrUnderline != 0)
	assert.True(t, attrs&tcell.AttrReverse != 0)
}

func TestRenderTerminal(t *testing.T) {
	term := vt10x.New(vt10x.WithSize(20, 5))
	_, err := term.Write([]byte("hello\r\nworld"))
	require.NoError(t, err)

	screen := tcell.NewSimulationScreen("UTF-8")
	require.NoError(t, screen.Init())
	defer screen.Fini()
	screen.SetSize(20, 5)

	cur, visible := renderTerminal(term, screen)
	screen.Show()
	assert.Equal(t, 5, cur.X)
	assert.Equal(t, 1, cur.Y)
	assert.True(t, visible)

	cells, width, height := screen.GetContents()
	require.Equal(t, 20, width)
	require.Equal(t, 5, height)

	line := func(row int) string {
		ret := make([]rune, 0, width)
		for x := 0; x < width; x++ {
			ret = append(ret, cells[row*width+x].Runes...)
		}
		return strings.TrimRight(string(ret), " ")
	}
	assert.Equal(t, "hello", line(0))
	assert.Equal(t, "world", line(1))
}
