// Demo code for the livepage tcell screen.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/pancsta/cview"
	lp "github.com/pancsta/livepage"

	tclive "github.com/pancsta/cview/tcell-livepage"
)

const demoText = `[::b]tcell + livepage[::-]

A cview application rendered as HTML in the browser.

 [red]red[-] [green]green[-] [blue]blue[-] [yellow]yellow[-] [::i]italic[::-] [::u]underline[::-] [::r]reverse[::-]

Unicode: 日本語 ěščřžýáíé ┌─┬─┐ └─┴─┘ ░▒▓█ ▸▹▶◀

Click, drag and scroll the terminal below.
Keyboard input goes to the application. Ctrl-C quits.

`

type demoState struct {
	mu       sync.Mutex
	mouseX   int
	mouseY   int
	buttons  tcell.ButtonMask
	action   cview.MouseAction
	lastKey  string
	mouseSet bool
}

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	flag.Parse()

	ctx := context.Background()
	screen, err := tclive.NewScreen(ctx, tclive.Opts{
		Cols: 100,
		Rows: 30,
		Live: lp.Opts{
			DiffHtml: true,
			Debounce: 10 * time.Millisecond,
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	app := cview.NewApplication()
	app.SetScreen(screen)
	app.EnableMouse(true)
	app.EnableBracketedPaste(false)

	state := &demoState{}
	app.SetMouseCapture(func(event *tcell.EventMouse, action cview.MouseAction) (*tcell.EventMouse, cview.MouseAction) {
		x, y := event.Position()
		state.mu.Lock()
		state.mouseX, state.mouseY = x, y
		state.buttons = event.Buttons()
		state.action = action
		state.mouseSet = true
		state.mu.Unlock()
		return event, action
	})
	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		state.mu.Lock()
		state.lastKey = keyString(event)
		state.mu.Unlock()
		return event
	})

	tv := cview.NewTextView()
	tv.SetDynamicColors(true)
	tv.SetText(demoText)
	app.SetRoot(tv, true)

	// Draw a status line with the last input and the grid size.
	app.SetAfterDrawFunc(func(scr tcell.Screen) {
		w, h := scr.Size()
		if w < 1 || h < 1 {
			return
		}

		state.mu.Lock()
		mouseX, mouseY := state.mouseX, state.mouseY
		buttons, action := state.buttons, state.action
		lastKey, mouseSet := state.lastKey, state.mouseSet
		state.mu.Unlock()

		msg := fmt.Sprintf("tcell-livepage | %dx%d | ctrl-c quits", w, h)
		if lastKey != "" {
			msg += " | key: " + lastKey
		}
		if mouseSet {
			msg += fmt.Sprintf(" | mouse: %d,%d %s %v", mouseX, mouseY, mouseActionName(action), buttons)
		}

		style := tcell.StyleDefault.
			Foreground(tcell.ColorBlack).
			Background(tcell.ColorLightGray)
		y := h - 1
		col := 0
		for _, r := range msg {
			if col >= w {
				break
			}
			scr.SetContent(col, y, r, nil, style)
			col++
		}
		for ; col < w; col++ {
			scr.SetContent(col, y, ' ', nil, style)
		}
	})

	go func() {
		log.Printf("serving the terminal on http://localhost%s", *addr)
		if err := http.ListenAndServe(*addr, screen.Handler()); err != nil {
			log.Fatal(err)
		}
	}()

	if err := app.Run(); err != nil {
		panic(err)
	}
}

func keyString(ev *tcell.EventKey) string {
	if ev.Key() == tcell.KeyRune {
		return fmt.Sprintf("%q", ev.Rune())
	}
	return ev.Name()
}

func mouseActionName(a cview.MouseAction) string {
	switch a {
	case cview.MouseMove:
		return "move"
	case cview.MouseLeftDown:
		return "left-down"
	case cview.MouseLeftUp:
		return "left-up"
	case cview.MouseLeftClick:
		return "left-click"
	case cview.MouseMiddleDown:
		return "middle-down"
	case cview.MouseMiddleUp:
		return "middle-up"
	case cview.MouseRightDown:
		return "right-down"
	case cview.MouseRightUp:
		return "right-up"
	case cview.MouseScrollUp:
		return "scroll-up"
	case cview.MouseScrollDown:
		return "scroll-down"
	case cview.MouseScrollLeft:
		return "scroll-left"
	case cview.MouseScrollRight:
		return "scroll-right"
	}
	return "mouse"
}
