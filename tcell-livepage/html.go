package tcelllivepage

import (
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
)

// runKey identifies a run of cells rendered with the same style. The cursor is
// rendered as a separate run.
type runKey struct {
	style  tcell.Style
	cursor bool
}

// gridHTML returns the HTML snapshot of the last completed frame.
func (s *Screen) gridHTML() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Fallback for direct calls before the first Show.
	if s.html == "" {
		s.html = s.renderGrid()
		s.dirty = false
	}
	return s.html
}

// renderGrid converts the current tcell grid into HTML. Cells are grouped into
// spans of equal style.
func (s *Screen) renderGrid() string {
	w, h := s.Size()
	if w <= 0 || h <= 0 {
		return `<div class="tcell-grid"></div>`
	}

	var b strings.Builder
	// Rough estimate: a space per cell, plus some markup.
	b.Grow(w*h + h*64 + 128)

	b.WriteString(`<div class="tcell-grid">`)
	b.WriteString(`<span class="tcell-probe" aria-hidden="true">M</span>`)

	cx, cy, cursorVisible := s.GetCursor()

	var (
		run    strings.Builder
		key    runKey
		hasRun bool
	)
	flush := func() {
		if !hasRun {
			return
		}
		text := run.String()
		hasRun = false
		run.Reset()
		if text == "" {
			return
		}
		writeSpan(&b, text, s.styleCSS(key.style), key.cursor, 0)
	}

	for y := 0; y < h; y++ {
		b.WriteString(`<div class="tcell-row">`)
		for x := 0; x < w; {
			str, style, width := s.Get(x, y)
			if width < 1 {
				width = 1
			}
			cursor := cursorVisible && x == cx && y == cy

			// Wide graphemes are wrapped to enforce their cell width, as font
			// fallbacks may render them narrower than two cells.
			if width > 1 {
				flush()
				writeSpan(&b, str, s.styleCSS(style), cursor, width)
				x += width
				continue
			}

			cellKey := runKey{style: style, cursor: cursor}
			if hasRun && cellKey != key {
				flush()
			}
			if !hasRun {
				key = cellKey
				hasRun = true
			}
			run.WriteString(str)
			x += width
		}
		flush()
		b.WriteString(`</div>`)
	}

	b.WriteString(`</div>`)
	return b.String()
}

// writeSpan writes a styled span. A non-zero width marks a wide cell, which is
// enforced via CSS to keep the grid aligned.
func writeSpan(b *strings.Builder, text, css string, cursor bool, width int) {
	if text == "" {
		return
	}

	class := ""
	if width > 1 {
		class = "tc-wide"
	}
	if cursor {
		if class != "" {
			class += " "
		}
		class += "tc-cursor"
	}

	if css == "" && class == "" {
		writeEscaped(b, text)
		return
	}

	b.WriteString(`<span`)
	if class != "" {
		b.WriteString(` class="`)
		b.WriteString(class)
		b.WriteString(`"`)
	}
	if css != "" || width > 1 {
		b.WriteString(` style="`)
		if width > 1 {
			b.WriteString("width:")
			b.WriteString(strconv.Itoa(width))
			b.WriteString("ch;")
		}
		b.WriteString(css)
		b.WriteString(`"`)
	}
	b.WriteString(`>`)
	writeEscaped(b, text)
	b.WriteString(`</span>`)
}

// styleCSS converts a tcell style into a CSS style. Default colors are omitted
// to keep the HTML small.
func (s *Screen) styleCSS(style tcell.Style) string {
	if css, ok := s.styles[style]; ok {
		return css
	}

	fg, bg, attrs := style.Decompose()
	if attrs&tcell.AttrReverse != 0 {
		fg, bg = bg, fg
	}

	var b strings.Builder
	if hex := colorHex(fg, s.theme.Fg); hex != s.theme.Fg.CSS() {
		b.WriteString("color:")
		b.WriteString(hex)
		b.WriteString(";")
	}
	if hex := colorHex(bg, s.theme.Bg); hex != s.theme.Bg.CSS() {
		b.WriteString("background-color:")
		b.WriteString(hex)
		b.WriteString(";")
	}

	var deco []string
	if attrs&tcell.AttrUnderline != 0 {
		deco = append(deco, "underline")
	}
	if attrs&tcell.AttrStrikeThrough != 0 {
		deco = append(deco, "line-through")
	}
	if len(deco) > 0 {
		b.WriteString("text-decoration:")
		b.WriteString(strings.Join(deco, " "))
		b.WriteString(";")
	}
	if attrs&tcell.AttrBold != 0 {
		b.WriteString("font-weight:700;")
	}
	if attrs&tcell.AttrDim != 0 {
		b.WriteString("opacity:.7;")
	}
	if attrs&tcell.AttrItalic != 0 {
		b.WriteString("font-style:italic;")
	}
	if attrs&tcell.AttrBlink != 0 {
		b.WriteString("animation:tc-blink 1s step-end infinite;")
	}

	css := b.String()
	s.styles[style] = css
	return css
}

// colorHex returns a CSS color, falling back to def for unset colors.
func colorHex(c, def tcell.Color) string {
	if !c.Valid() {
		c = def
	}
	return c.CSS()
}

// writeEscaped writes s with HTML special characters escaped.
func writeEscaped(b *strings.Builder, s string) {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&#39;")
		default:
			b.WriteByte(s[i])
		}
	}
}

// injectHead adds tags at the end of <head>, or at the start of the document.
func injectHead(doc, tags string) string {
	i := strings.Index(strings.ToLower(doc), "</head>")
	if i < 0 {
		return tags + doc
	}
	return doc[:i] + tags + doc[i:]
}
