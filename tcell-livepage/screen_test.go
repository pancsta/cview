package tcelllivepage

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gdamore/tcell/v2"
	lp "github.com/pancsta/livepage"
)

func newTestServer(t *testing.T) (*Screen, *httptest.Server) {
	t.Helper()
	return newTestServerOpts(t, nil)
}

func newTestServerOpts(
	t *testing.T, configure func(*Opts),
) (*Screen, *httptest.Server) {
	t.Helper()

	opts := Opts{
		Cols: 20,
		Rows: 6,
		Live: lp.Opts{Debounce: 5 * time.Millisecond},
	}
	if configure != nil {
		configure(&opts)
	}

	ctx, cancel := context.WithCancel(context.Background())
	s, err := NewScreen(ctx, opts)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.Fini()
		cancel()
	})

	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)

	return s, srv
}

func TestPage(t *testing.T) {
	_, srv := newTestServer(t)

	res, err := srv.Client().Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("unexpected status %d", res.StatusCode)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)

	for _, want := range []string{
		`id="tcell-screen"`,
		`class="tcell-screen"`,
		`data-cols="20"`,
		`data-rows="6"`,
		`class="tcell-grid"`,
		`<style>`,
		`__tcellScreen`,
		lp.PathAssets + "livepage.js",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %q in page", want)
		}
	}
}

func dial(t *testing.T, srv *httptest.Server) (*websocket.Conn, context.Context) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+lp.PathWs, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.CloseNow() })

	return c, ctx
}

func readHTML(t *testing.T, ctx context.Context, c *websocket.Conn, want string) string {
	t.Helper()
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var msg struct {
			Type string
			Html string
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Fatal(err)
		}
		if msg.Type != "html" {
			continue
		}
		if want == "" || strings.Contains(msg.Html, want) {
			return msg.Html
		}
	}
}

func sendEvent(t *testing.T, ctx context.Context, c *websocket.Conn, typ string, in domInput) {
	t.Helper()
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := json.Marshal(map[string]string{"type": typ, "data": string(data)})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Write(ctx, websocket.MessageText, msg); err != nil {
		t.Fatal(err)
	}
}

func pollEvent(t *testing.T, s *Screen) tcell.Event {
	t.Helper()
	ch := make(chan tcell.Event, 1)
	go func() {
		ch <- s.PollEvent()
	}()

	select {
	case ev := <-ch:
		if ev == nil {
			t.Fatal("nil event")
		}
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("event timeout")
		return nil
	}
}

func TestScreenHTMLLiveUpdate(t *testing.T) {
	s, srv := newTestServer(t)
	c, ctx := dial(t, srv)

	readHTML(t, ctx, c, `class="tcell-grid"`)

	s.SetContent(0, 0, 'Z', nil, tcell.StyleDefault.Foreground(tcell.ColorRed))
	s.Show()

	html := readHTML(t, ctx, c, ">Z<")
	if !strings.Contains(html, "color:#FF0000;") {
		t.Fatalf("missing style in %s", html)
	}
}

func TestScreenInputEvents(t *testing.T) {
	s, srv := newTestServer(t)
	c, ctx := dial(t, srv)
	readHTML(t, ctx, c, `class="tcell-grid"`)

	// key event
	sendEvent(t, ctx, c, typeKey, domInput{Key: "a"})
	ev := pollEvent(t, s)
	key, ok := ev.(*tcell.EventKey)
	if !ok {
		t.Fatalf("unexpected event type %T", ev)
	}
	if key.Key() != tcell.KeyRune || key.Rune() != 'a' {
		t.Fatalf("unexpected key %v %q", key.Key(), key.Rune())
	}

	// mouse event with cell coordinates
	sendEvent(t, ctx, c, typeMouse, domInput{Action: "down", X: 3, Y: 2, Buttons: 1})
	ev = pollEvent(t, s)
	mouse, ok := ev.(*tcell.EventMouse)
	if !ok {
		t.Fatalf("unexpected event type %T", ev)
	}
	if x, y := mouse.Position(); x != 3 || y != 2 {
		t.Fatalf("unexpected position %d,%d", x, y)
	}
}

func TestScreenResizeEvent(t *testing.T) {
	s, srv := newTestServer(t)
	c, ctx := dial(t, srv)
	readHTML(t, ctx, c, `class="tcell-grid"`)

	sendEvent(t, ctx, c, typeResize, domInput{Cols: 30, Rows: 10})
	ev := pollEvent(t, s)
	resize, ok := ev.(*tcell.EventResize)
	if !ok {
		t.Fatalf("unexpected event type %T", ev)
	}
	if w, h := resize.Size(); w != 30 || h != 10 {
		t.Fatalf("unexpected resize %dx%d", w, h)
	}
	if w, h := s.Size(); w != 30 || h != 10 {
		t.Fatalf("unexpected size %dx%d", w, h)
	}
}

func TestLazyRender(t *testing.T) {
	s, srv := newTestServer(t)

	// The first frame is primed even without a client.
	s.SetContent(0, 0, 'A', nil, tcell.StyleDefault)
	s.Show()

	// No clients: rendering is skipped.
	s.SetContent(0, 0, 'B', nil, tcell.StyleDefault)
	s.Show()

	c, ctx := dial(t, srv)
	initial := readHTML(t, ctx, c, `class="tcell-grid"`)
	if !strings.Contains(initial, `<div class="tcell-row">A`) {
		t.Fatalf("expected the primed frame, got %s", initial)
	}

	// A client with an unchanged viewport forces a fresh frame.
	sendEvent(t, ctx, c, typeResize, domInput{Cols: 20, Rows: 6})
	readHTML(t, ctx, c, `<div class="tcell-row">B`)
}

func TestRefreshInterval(t *testing.T) {
	s, srv := newTestServerOpts(t, func(opts *Opts) {
		opts.RefreshInterval = 50 * time.Millisecond
	})
	c, ctx := dial(t, srv)
	readHTML(t, ctx, c, `class="tcell-grid"`)

	// The first Show primes, the second is coalesced into a trailing refresh.
	s.SetContent(0, 0, 'A', nil, tcell.StyleDefault)
	s.Show()
	s.SetContent(0, 0, 'B', nil, tcell.StyleDefault)
	s.Show()

	readHTML(t, ctx, c, `<div class="tcell-row">B`)
}

func TestMinViewport(t *testing.T) {
	s, srv := newTestServer(t)
	c1, ctx := dial(t, srv)
	c2, _ := dial(t, srv)

	// two clients share the smallest viewport
	sendEvent(t, ctx, c1, typeResize, domInput{Cols: 30, Rows: 10})
	sendEvent(t, ctx, c2, typeResize, domInput{Cols: 80, Rows: 24})
	waitSize(t, s, 30, 10)

	// a wider client does not grow the grid
	sendEvent(t, ctx, c2, typeResize, domInput{Cols: 40, Rows: 12})
	waitSize(t, s, 30, 10)

	// the smallest client leaves, the grid follows the remaining one
	_ = c1.CloseNow()
	waitSize(t, s, 40, 12)
}

func waitSize(t *testing.T, s *Screen, cols, rows int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if w, h := s.Size(); w == cols && h == rows {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	w, h := s.Size()
	t.Fatalf("unexpected size %dx%d, want %dx%d", w, h, cols, rows)
}
