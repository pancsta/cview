//go:build !windows

package tcelllivepage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/gdamore/tcell/v2"
	"github.com/hinshun/vt10x"
	lp "github.com/pancsta/livepage"
)

// vt10x glyph attribute bits (unexported in vt10x).
const (
	vtAttrReverse   = 1 << 0
	vtAttrUnderline = 1 << 1
	vtAttrBold      = 1 << 2
	vtAttrItalic    = 1 << 4
)

// RunCmd serves cmd over HTTP using livepage. The command runs on a Unix PTY,
// so programs see a real terminal; its output is mirrored into a [Screen]. It
// blocks until the command exits or ctx is canceled.
func RunCmd(ctx context.Context, cmd *exec.Cmd, opts CmdOpts) error {
	// TODO migrate to github.com/aymanbagabas/go-pty for win support, also slog, return HTTP handler
	if opts.Printf == nil {
		opts.Printf = func(string, ...any) {}
	}
	if cmd.Env == nil {
		cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	} else {
		cmd.Env = append(cmd.Env, "TERM=xterm-256color")
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	screen, err := NewScreen(ctx, Opts{
		Title:           opts.Title,
		RefreshInterval: 50 * time.Millisecond,
		Live: lp.Opts{
			Debounce: 50 * time.Millisecond,
		},
	})
	if err != nil {
		return fmt.Errorf("livepage screen: %w", err)
	}
	screen.Sync()

	lis, err := net.Listen("tcp", opts.Addr)
	if err != nil {
		screen.Fini()
		return fmt.Errorf("livepage listen: %w", err)
	}

	srv := &http.Server{
		Handler:           screen.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := srv.Serve(lis); err != nil &&
			!errors.Is(err, http.ErrServerClosed) {

			opts.Printf("livepage: %s", err)
		}
	}()
	opts.Printf("Livepage: http://%s/", displayAddr(lis.Addr()))

	cols, rows := screen.Size()
	if cols <= 0 {
		cols = DefaultCols
	}
	if rows <= 0 {
		rows = DefaultRows
	}

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
		Rows: uint16(rows),
		Cols: uint16(cols),
	})
	if err != nil {
		_ = srv.Close()
		screen.Fini()
		return fmt.Errorf("livepage pty: %w", err)
	}

	bridge := &livepageBridge{
		screen: screen,
		term:   vt10x.New(vt10x.WithSize(cols, rows)),
		ptmx:   ptmx,
	}

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		bridge.readLoop()
	}()
	go func() {
		defer wg.Done()
		bridge.renderLoop(ctx)
	}()
	go func() {
		defer wg.Done()
		bridge.inputLoop()
	}()

	// wait for the child to exit or for a signal
	childDone := make(chan struct{})
	var childErr error
	go func() {
		childErr = cmd.Wait()
		close(childDone)
	}()

	interrupted := false
	select {
	case <-ctx.Done():
		interrupted = true
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-childDone:
		case <-time.After(2 * time.Second):
			_ = cmd.Process.Kill()
			<-childDone
		}
	case <-childDone:
	}

	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(
		context.Background(), 3*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
	_ = ptmx.Close()
	screen.Fini()
	wg.Wait()

	if interrupted {
		return nil
	}
	if childErr != nil && !errors.Is(childErr, os.ErrProcessDone) {
		return fmt.Errorf("livepage cmd exited: %w\n\ncmd: %s\n\noutput:\n%s",
			childErr, cmd.String(), bridge.output())
	}

	return nil
}

// livepageBridge mirrors a PTY-backed terminal into a livepage screen.
type livepageBridge struct {
	screen *Screen
	term   vt10x.Terminal
	ptmx   *os.File

	// out holds the last [maxOut] bytes of the child's output, for errors.
	out []byte

	termMu  sync.Mutex
	writeMu sync.Mutex
	dirty   atomic.Bool
}

// maxOut is the number of child output bytes kept for error reporting.
const maxOut = 4096

// output returns the tail of the child's output. Read it only after the read
// loop has finished.
func (b *livepageBridge) output() string {
	return string(b.out)
}

// readLoop feeds the child's output into the virtual terminal.
func (b *livepageBridge) readLoop() {
	buf := make([]byte, 32*1024)
	for {
		n, err := b.ptmx.Read(buf)
		if n > 0 {
			chunk := buf[:n]

			b.out = append(b.out, chunk...)
			if len(b.out) > maxOut {
				b.out = b.out[len(b.out)-maxOut:]
			}

			b.termMu.Lock()
			_, _ = b.term.Write(chunk)
			var reply string
			if bytes.Contains(chunk, []byte("\x1b[6n")) {
				cur := b.term.Cursor()
				reply = fmt.Sprintf("\x1b[%d;%dR", cur.Y+1, cur.X+1)
			}
			b.termMu.Unlock()

			if reply != "" {
				b.writePTY([]byte(reply))
			}
			b.dirty.Store(true)
		}
		if err != nil {
			return
		}
	}
}

// renderLoop copies the virtual terminal grid into the livepage screen.
func (b *livepageBridge) renderLoop(ctx context.Context) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	b.render()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !b.dirty.Swap(false) {
				continue
			}
			b.render()
		}
	}
}

func (b *livepageBridge) render() {
	b.termMu.Lock()
	cur, cursorVisible := renderTerminal(b.term, b.screen)
	b.termMu.Unlock()

	if cursorVisible {
		b.screen.ShowCursor(cur.X, cur.Y)
	} else {
		b.screen.HideCursor()
	}
	// Sync (not Show) keeps latestHtml current while no browser is connected,
	// so reconnecting tabs get the live grid instead of a stale frame.
	b.screen.Sync()
}

// renderTerminal copies a vt10x grid into a tcell screen and returns the
// cursor state.
func renderTerminal(
	term vt10x.Terminal, screen tcell.Screen,
) (vt10x.Cursor, bool) {
	termCols, termRows := term.Size()
	cur := term.Cursor()
	cursorVisible := term.CursorVisible()

	scrCols, scrRows := screen.Size()
	cols := min(termCols, scrCols)
	rows := min(termRows, scrRows)

	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			glyph := term.Cell(x, y)
			char := glyph.Char
			if char == 0 {
				char = ' '
			}
			screen.SetContent(x, y, char, nil, vtStyle(glyph))
		}
	}

	return cur, cursorVisible
}

// inputLoop forwards livepage keyboard and resize events to the PTY.
func (b *livepageBridge) inputLoop() {
	for {
		ev := b.screen.PollEvent()
		if ev == nil {
			return
		}

		switch ev := ev.(type) {
		case *tcell.EventResize:
			cols, rows := ev.Size()
			_ = pty.Setsize(b.ptmx, &pty.Winsize{
				Rows: uint16(rows),
				Cols: uint16(cols),
			})
			b.termMu.Lock()
			b.term.Resize(cols, rows)
			b.termMu.Unlock()
			b.dirty.Store(true)

		case *tcell.EventKey:
			if data := encodeKey(ev); len(data) > 0 {
				b.writePTY(data)
			}
		}
	}
}

func (b *livepageBridge) writePTY(data []byte) {
	b.writeMu.Lock()
	defer b.writeMu.Unlock()

	_, _ = b.ptmx.Write(data)
}

// vtStyle converts a vt10x glyph into a tcell style.
func vtStyle(glyph vt10x.Glyph) tcell.Style {
	style := tcell.StyleDefault.
		Foreground(vtColor(glyph.FG)).
		Background(vtColor(glyph.BG))

	if glyph.Mode&vtAttrBold != 0 {
		style = style.Bold(true)
	}
	if glyph.Mode&vtAttrItalic != 0 {
		style = style.Italic(true)
	}
	if glyph.Mode&vtAttrUnderline != 0 {
		style = style.Underline(true)
	}
	if glyph.Mode&vtAttrReverse != 0 {
		style = style.Reverse(true)
	}

	return style
}

// vtColor converts a vt10x color into a tcell color.
func vtColor(color vt10x.Color) tcell.Color {
	switch color {
	case vt10x.DefaultFG, vt10x.DefaultBG, vt10x.DefaultCursor:
		return tcell.ColorDefault
	}
	if color < 256 {
		return tcell.PaletteColor(int(color))
	}

	return tcell.NewHexColor(int32(color))
}

// displayAddr turns wildcard listen addresses into something clickable.
func displayAddr(addr net.Addr) string {
	host, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}
	if host == "" || host == "::" || host == "0.0.0.0" {
		host = "localhost"
	}

	return net.JoinHostPort(host, port)
}
