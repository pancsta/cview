//go:build !windows

package tcelllivepage

import "github.com/gdamore/tcell/v2"

// encodeKey converts a livepage tcell key event into terminal input bytes.
func encodeKey(ev *tcell.EventKey) []byte {
	if ev == nil {
		return nil
	}

	var prefix []byte
	if ev.Modifiers()&tcell.ModAlt != 0 {
		prefix = []byte{0x1b}
	}

	switch ev.Key() {
	case tcell.KeyRune:
		r := ev.Rune()
		if ev.Modifiers()&tcell.ModCtrl != 0 {
			if r >= 'a' && r <= 'z' {
				return append(prefix, byte(r-'a'+1))
			}
			if r >= 'A' && r <= 'Z' {
				return append(prefix, byte(r-'A'+1))
			}
		}
		return append(prefix, []byte(string(r))...)

	case tcell.KeyEnter:
		return append(prefix, '\r')
	case tcell.KeyTab:
		return append(prefix, '\t')
	case tcell.KeyBacktab:
		return append(prefix, '\x1b', '[', 'Z')
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		return append(prefix, 0x7f)
	case tcell.KeyEsc:
		return append(prefix, 0x1b)
	}

	// control keys (KeyCtrlSpace..KeyCtrlUnderscore are offset by 64)
	if ev.Key() >= tcell.KeyCtrlSpace && ev.Key() <= tcell.KeyCtrlUnderscore {
		return append(prefix, byte(ev.Key()-tcell.KeyCtrlSpace))
	}

	seq := specialKey(ev.Key())
	if len(seq) == 0 {
		return nil
	}
	if len(prefix) > 0 {
		return append(prefix, seq...)
	}

	return seq
}

// specialKey returns the xterm sequence for a named key.
func specialKey(key tcell.Key) []byte {
	switch key {
	case tcell.KeyUp:
		return []byte("\x1b[A")
	case tcell.KeyDown:
		return []byte("\x1b[B")
	case tcell.KeyRight:
		return []byte("\x1b[C")
	case tcell.KeyLeft:
		return []byte("\x1b[D")
	case tcell.KeyHome:
		return []byte("\x1b[H")
	case tcell.KeyEnd:
		return []byte("\x1b[F")
	case tcell.KeyPgUp:
		return []byte("\x1b[5~")
	case tcell.KeyPgDn:
		return []byte("\x1b[6~")
	case tcell.KeyInsert:
		return []byte("\x1b[2~")
	case tcell.KeyDelete:
		return []byte("\x1b[3~")
	case tcell.KeyF1:
		return []byte("\x1bOP")
	case tcell.KeyF2:
		return []byte("\x1bOQ")
	case tcell.KeyF3:
		return []byte("\x1bOR")
	case tcell.KeyF4:
		return []byte("\x1bOS")
	case tcell.KeyF5:
		return []byte("\x1b[15~")
	case tcell.KeyF6:
		return []byte("\x1b[17~")
	case tcell.KeyF7:
		return []byte("\x1b[18~")
	case tcell.KeyF8:
		return []byte("\x1b[19~")
	case tcell.KeyF9:
		return []byte("\x1b[20~")
	case tcell.KeyF10:
		return []byte("\x1b[21~")
	case tcell.KeyF11:
		return []byte("\x1b[23~")
	case tcell.KeyF12:
		return []byte("\x1b[24~")
	}

	return nil
}
