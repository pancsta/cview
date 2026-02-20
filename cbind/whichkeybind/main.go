package main

import (
	"fmt"
	"os"

	"github.com/pancsta/cview/cbind"
	"github.com/pancsta/tcell-v2"
)

func main() {
	tcell.SetEncodingFallback(tcell.EncodingFallbackASCII)
	s, e := tcell.NewScreen()
	if e != nil {
		fmt.Fprintf(os.Stderr, "%v\n", e)
		os.Exit(1)
	}
	if e = s.Init(); e != nil {
		fmt.Fprintf(os.Stderr, "%v\n", e)
		os.Exit(1)
	}

	quit := make(chan struct{})

	quitApp := func(ev *tcell.EventKey) *tcell.EventKey {
		quit <- struct{}{}
		return nil
	}

	configuration := cbind.NewConfiguration()
	configuration.SetKey(tcell.ModNone, tcell.KeyEscape, quitApp)
	configuration.SetRune(tcell.ModCtrl, 'c', quitApp)

	s.SetStyle(tcell.StyleDefault.
		Foreground(tcell.ColorWhite).
		Background(tcell.ColorBlack))
	s.Clear()

	go func() {
		for {
			ev := s.PollEvent()
			switch ev := ev.(type) {
			case *tcell.EventResize:
				s.Sync()
			case *tcell.EventKey:
				s.SetStyle(tcell.StyleDefault.
					Foreground(tcell.ColorWhite).
					Background(tcell.ColorBlack))
				s.Clear()

				putln(s, 0, fmt.Sprintf("Event: %d %d %d", ev.Modifiers(), ev.Key(), ev.Rune()))

				str, err := cbind.Encode(ev.Modifiers(), ev.Key(), ev.Rune())
				if err != nil {
					str = fmt.Sprintf("error: %s", err)
				}
				putln(s, 2, str)

				mod, key, ch, err := cbind.Decode(str)
				if err != nil {
					putln(s, 4, err.Error())
				} else {
					putln(s, 4, fmt.Sprintf("Re-encoded as: %d %d %d", mod, key, ch))
				}

				configuration.Capture(ev)

				s.Sync()
			}
		}
	}()
	s.Show()

	<-quit
	s.Fini()
}

// putln and puts functions are copied from the tcell unicode demo.
// Apache License, Version 2.0

func putln(s tcell.Screen, y int, str string) {
	puts(s, tcell.StyleDefault, 0, y, str)
}

func puts(s tcell.Screen, style tcell.Style, x, y int, str string) {
	i := 0
	var deferred []rune
	dwidth := 0
	zwj := false
	for _, r := range str {
		if r == '\u200d' {
			if len(deferred) == 0 {
				deferred = append(deferred, ' ')
				dwidth = 1
			}
			deferred = append(deferred, r)
			zwj = true
			continue
		}
		if zwj {
			deferred = append(deferred, r)
			zwj = false
			continue
		}
		if len(deferred) != 0 {
			s.SetContent(x+i, y, deferred[0], deferred[1:], style)
			i += dwidth
		}
		deferred = nil
		dwidth = 1
		deferred = append(deferred, r)
	}
	if len(deferred) != 0 {
		s.SetContent(x+i, y, deferred[0], deferred[1:], style)
		i += dwidth
	}
}
