package cview

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

// drawCounter counts Draw calls.
type drawCounter struct {
	*Box
	draws int
}

func (d *drawCounter) Draw(_ tcell.Screen) {
	d.draws++
}

func newDrawTestApp(t *testing.T) (*Application, *drawCounter) {
	t.Helper()

	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(screen.Fini)
	screen.SetSize(20, 5)

	app := NewApplication()
	root := &drawCounter{Box: NewBox()}
	app.SetScreen(screen)
	app.SetRoot(root, false)

	return app, root
}

func TestDrawOnKey(t *testing.T) {
	key := tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone)

	t.Run("capture consumed", func(t *testing.T) {
		app, root := newDrawTestApp(t)
		app.SetInputCapture(func(*tcell.EventKey) *tcell.EventKey {
			return nil
		})

		app.handle(key)
		if root.draws != 1 {
			t.Fatalf("expected 1 draw, got %d", root.draws)
		}

		app.SetDrawOnKey(false)
		app.handle(key)
		if root.draws != 1 {
			t.Fatalf("expected no draw after disabling, got %d", root.draws)
		}
	})

	t.Run("capture forwarded", func(t *testing.T) {
		app, root := newDrawTestApp(t)
		app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
			return event
		})

		app.handle(key)
		if root.draws != 1 {
			t.Fatalf("expected 1 draw, got %d", root.draws)
		}

		app.SetDrawOnKey(false)
		app.handle(key)
		if root.draws != 1 {
			t.Fatalf("expected no draw after disabling, got %d", root.draws)
		}
	})
}
