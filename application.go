package cview

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"

	ssApp "github.com/pancsta/cview/states"
)

const (
	// The size of the event/update channels.
	queueSize = 100

	// The minimum duration between resize event callbacks.
	resizeEventThrottle = 50 * time.Millisecond

	// appHandlerTimeout is the time a single event loop handler may take before
	// it is aborted by the state machine.
	appHandlerTimeout = 30 * time.Second
	// appEvalTimeout is the time the state machine waits for an Eval func.
	appEvalTimeout = 30 * time.Second
)

// Application represents the top node of an application.
//
// It is not strictly required to use this class as none of the other classes
// depend on it. However, it provides useful tools to set up an application and
// plays nicely with all widgets.
//
// The event loop of the application is driven by an asyncmachine-go state
// machine, see [Application.Mach]. Input events, queued updates, and screen
// replacements are serialized with [am.Machine.Eval], which allows primitives
// to operate without locks during the event loop. Calls from other goroutines
// should be queued with [Application.QueueUpdate] or
// [Application.QueueEvent].
//
// The following command displays a primitive p on the screen until Ctrl-C is
// pressed:
//
//	if err := cview.NewApplication().SetRoot(p, true).Run(); err != nil {
//	    panic(err)
//	}
type Application struct {
	// Mach is the state machine driving the application's event loop.
	Mach *am.Machine

	// screen holds the application's screen. Apart from Run(), this value
	// should never be set directly. Always use SetScreen to replace it.
	screen atomic.Pointer[tcell.Screen]

	// width, height hold the size of the application's screen.
	width, height atomic.Int32

	// focus holds the primitive which currently has the keyboard focus.
	focus atomic.Pointer[primitiveRef]

	// root holds the root primitive to be seen on the screen.
	root atomic.Pointer[primitiveRef]

	// rootFullscreen tells whether the application resizes the root primitive.
	rootFullscreen atomic.Bool

	// callbacks holds the optional user callbacks of the application.
	callbacks atomic.Pointer[appCallbacks]

	// enableBracketedPaste tells whether to enable bracketed paste mode.
	enableBracketedPaste atomic.Bool

	// enableMouse tells whether to enable mouse events.
	enableMouse atomic.Bool

	// doubleClickInterval specifies the maximum time between clicks to
	// register a double click rather than a single click.
	doubleClickInterval atomic.Int64

	// Time a resize event was last processed. Event loop only.
	lastResize time.Time
	// Timer limiting how quickly resize events are processed. Event loop only.
	throttleResize *time.Timer

	// Used to send screen events from separate goroutine to the main event
	// loop.
	events chan tcell.Event

	// Functions queued from goroutines, used to serialize updates to
	// primitives.
	updates chan func()

	// The last mouse state. Event loop only.
	mouseCapturingPrimitive Primitive        // A Primitive returned by a MouseHandler which will capture future mouse events.
	lastMouseX, lastMouseY  int              // The last position of the mouse.
	mouseDownX, mouseDownY  int              // The position of the mouse when its button was last pressed.
	lastMouseClick          time.Time        // The time when a mouse button was last clicked.
	lastMouseButtons        tcell.ButtonMask // The last mouse button state.

	// runErr holds the error which terminated the event loop.
	runErr error
}

// primitiveRef is a comparable holder for a Primitive, so it can be stored in
// an atomic pointer.
type primitiveRef struct {
	p Primitive
}

// appCallbacks holds the optional callbacks of an Application.
type appCallbacks struct {
	inputCapture func(event *tcell.EventKey) *tcell.EventKey
	mouseCapture func(event *tcell.EventMouse, action MouseAction) (*tcell.EventMouse, MouseAction)
	beforeFocus  func(p Primitive) bool
	afterFocus   func(p Primitive)
	beforeDraw   func(screen tcell.Screen) bool
	afterDraw    func(screen tcell.Screen)
	afterResize  func(width int, height int)
	// drawOnKey redraws the screen after a key event. Defaults to true.
	drawOnKey bool
}

// NewApplication creates and returns a new application with its own state
// machine.
func NewApplication() *Application {
	app, err := NewApplicationWithMach(nil)
	if err != nil {
		panic(err)
	}
	return app
}

// NewApplicationWithMach creates and returns a new application using the passed
// state machine. If mach is nil, a new machine is created.
//
// The machine's schema must implement states.AppSchema, usually by merging it
// with a custom schema:
//
//	schema := states.AppSchema.Merge(customSchema)
//	names := states.AppStates.Names().Add(customNames)
//
// The caller keeps ownership of a passed machine, i.e. the application will not
// dispose it.
func NewApplicationWithMach(mach *am.Machine) (*Application, error) {
	a := &Application{
		events:  make(chan tcell.Event, queueSize),
		updates: make(chan func(), queueSize),
	}
	a.enableBracketedPaste.Store(true)

	var err error
	if mach == nil {
		mach, err = a.newMachine()
		if err != nil {
			return nil, err
		}
	}
	if err := a.bindMachine(mach); err != nil {
		return nil, err
	}

	return a, nil
}

// newMachine creates a new state machine for the application.
func (a *Application) newMachine() (*am.Machine, error) {
	mach, err := am.NewCommon(context.Background(), "", ssApp.AppSchema,
		ssApp.AppStates.Names(), nil, nil, &am.Opts{
			HandlerTimeout: appHandlerTimeout,
			QueueLimit:     1000,
		})
	if err != nil {
		return nil, err
	}
	mach.EvalTimeout = appEvalTimeout
	mach.SemLogger().SetArgsMapper(am.NewLogArgsMapper(0, nil))

	return mach, nil
}

// bindMachine validates the passed machine and binds the application handlers.
func (a *Application) bindMachine(mach *am.Machine) error {
	if mach == nil {
		return errors.New("cview: nil machine")
	}
	if mach.IsDisposed() {
		return errors.New("cview: machine is disposed")
	}
	if !mach.Schema().Implements(ssApp.AppSchema) {
		missing := ssApp.AppStates.Names().Sub(mach.StateNames())
		return fmt.Errorf("cview: machine schema is missing app states: %v",
			missing)
	}

	handlers := &appHandlers{
		ExceptionHandler: &am.ExceptionHandler{},
		app:              a,
	}
	if _, err := mach.HandlersBind(handlers, am.BindOpts{Id: "cview:app"}); err != nil {
		return fmt.Errorf("cview: bind app handlers: %w", err)
	}

	a.Mach = mach
	return nil
}

// HandlePanic (when deferred at the start of a goroutine) handles panics
// gracefully. The terminal is returned to its original state before the panic
// message is printed.
//
// Panics may only be handled by the panicking goroutine. Because of this,
// HandlePanic must be deferred at the start of each goroutine (including main).
func (a *Application) HandlePanic() {
	p := recover()
	if p == nil {
		return
	}

	a.finalizeScreen()

	panic(p)
}

// SetInputCapture sets a function which captures all key events before they are
// forwarded to the key event handler of the primitive which currently has
// focus. This function can then choose to forward that key event (or a
// different one) by returning it or stop the key event processing by returning
// nil.
//
// Note that this also affects the default event handling of the application
// itself: Such a handler can intercept the Ctrl-C event which closes the
// application.
func (a *Application) SetInputCapture(capture func(event *tcell.EventKey) *tcell.EventKey) {
	a.setCallbacks(func(c *appCallbacks) {
		c.inputCapture = capture
	})
}

// GetInputCapture returns the function installed with SetInputCapture() or nil
// if no such function has been installed.
func (a *Application) GetInputCapture() func(event *tcell.EventKey) *tcell.EventKey {
	return a.getCallbacks().inputCapture
}

// SetDrawOnKey sets whether the application redraws the screen automatically
// after handling a key event. Disable it when key handlers redraw the affected
// primitives themselves (e.g. via [Application.Draw]). Defaults to true.
func (a *Application) SetDrawOnKey(enable bool) {
	a.setCallbacks(func(c *appCallbacks) {
		c.drawOnKey = enable
	})
}

// GetDrawOnKey returns whether the application redraws after key events.
func (a *Application) GetDrawOnKey() bool {
	return a.getCallbacks().drawOnKey
}

// SetMouseCapture sets a function which captures mouse events (consisting of
// the original tcell mouse event and the semantic mouse action) before they are
// forwarded to the appropriate mouse event handler. This function can then
// choose to forward that event (or a different one) by returning it or stop
// the event processing by returning a nil mouse event.
func (a *Application) SetMouseCapture(capture func(event *tcell.EventMouse, action MouseAction) (*tcell.EventMouse, MouseAction)) {
	a.setCallbacks(func(c *appCallbacks) {
		c.mouseCapture = capture
	})
}

// GetMouseCapture returns the function installed with SetMouseCapture() or nil
// if no such function has been installed.
func (a *Application) GetMouseCapture() func(event *tcell.EventMouse, action MouseAction) (*tcell.EventMouse, MouseAction) {
	return a.getCallbacks().mouseCapture
}

// SetDoubleClickInterval sets the maximum time between clicks to register a
// double click rather than a single click. A standard duration is provided as
// StandardDoubleClick. No interval is set by default, disabling double clicks.
func (a *Application) SetDoubleClickInterval(interval time.Duration) {
	a.doubleClickInterval.Store(int64(interval))
}

// SetScreen allows you to provide your own tcell.Screen object. For most
// applications, this is not needed and you should be familiar with
// tcell.Screen when using this function.
//
// This function is typically called before the first call to Run(). Init() need
// not be called on the screen.
func (a *Application) SetScreen(screen tcell.Screen) {
	if screen == nil {
		return // Invalid input. Do nothing.
	}

	if a.Mach.Is1(ssApp.AppStates.Start) {
		// Run() is already in progress. Exchange the screen on the event loop.
		a.Mach.Add1(ssApp.AppStates.CviewScreenReplacing,
			am.Pass(&AScreen{Screen: screen}))
		return
	}

	// Run() has not been called yet.
	a.setScreen(screen)
}

// GetScreen returns the current tcell.Screen of the application. This value is
// only available after calling Init or Run.
func (a *Application) GetScreen() tcell.Screen {
	return a.getScreen()
}

// GetScreenSize returns the size of the application's screen. These values are
// only available after calling Init or Run.
func (a *Application) GetScreenSize() (width, height int) {
	return int(a.width.Load()), int(a.height.Load())
}

// Init initializes the application screen. Calling Init before running is not
// required. Its primary use is to populate screen dimensions before running an
// application.
func (a *Application) Init() error {
	return a.init()
}

func (a *Application) init() error {
	if screen := a.getScreen(); screen != nil {
		// the screen was provided via SetScreen, it's already initialized
		width, height := screen.Size()
		a.width.Store(int32(width))
		a.height.Store(int32(height))
		return nil
	}

	screen, err := tcell.NewScreen()
	if err != nil {
		return err
	}
	if err = screen.Init(); err != nil {
		return err
	}
	a.setScreen(screen)
	width, height := screen.Size()
	a.width.Store(int32(width))
	a.height.Store(int32(height))
	if a.enableBracketedPaste.Load() {
		screen.EnablePaste()
	}
	if a.enableMouse.Load() {
		screen.EnableMouse()
	}
	return nil
}

// EnableBracketedPaste enables bracketed paste mode, which is enabled by default.
func (a *Application) EnableBracketedPaste(enable bool) {
	if enable != a.enableBracketedPaste.Load() {
		if screen := a.getScreen(); screen != nil {
			if enable {
				screen.EnablePaste()
			} else {
				screen.DisablePaste()
			}
		}
	}
	a.enableBracketedPaste.Store(enable)
}

// EnableMouse enables mouse events.
func (a *Application) EnableMouse(enable bool) {
	if enable != a.enableMouse.Load() {
		if screen := a.getScreen(); screen != nil {
			if enable {
				screen.EnableMouse()
			} else {
				screen.DisableMouse()
			}
		}
	}
	a.enableMouse.Store(enable)
}

// Run starts the application and thus the event loop. This function returns
// when Stop() was called.
func (a *Application) Run() error {
	a.runErr = nil
	if a.Mach.Add1(ssApp.AppStates.Start, nil) == am.Canceled {
		return a.runErr
	}

	// wait until the event loop stopped
	<-a.Mach.When1(ssApp.AppStates.CviewStopped, nil)

	return a.runErr
}

// fireMouseActions analyzes the provided mouse event, derives mouse actions
// from it and then forwards them to the corresponding primitives.
func (a *Application) fireMouseActions(event *tcell.EventMouse) (consumed, isMouseDownAction bool) {
	// We want to relay follow-up events to the same target primitive.
	var targetPrimitive Primitive
	mouseCapture := a.getCallbacks().mouseCapture
	root := a.getRoot()
	doubleClickInterval := time.Duration(a.doubleClickInterval.Load())

	// Helper function to fire a mouse action.
	fire := func(action MouseAction) {
		switch action {
		case MouseLeftDown, MouseMiddleDown, MouseRightDown:
			isMouseDownAction = true
		}

		// Intercept event.
		if mouseCapture != nil {
			event, action = mouseCapture(event, action)
			if event == nil {
				consumed = true
				return // Don't forward event.
			}
		}

		// Determine the target primitive.
		var primitive, capturingPrimitive Primitive
		if a.mouseCapturingPrimitive != nil {
			primitive = a.mouseCapturingPrimitive
			targetPrimitive = a.mouseCapturingPrimitive
		} else if targetPrimitive != nil {
			primitive = targetPrimitive
		} else {
			primitive = root
		}
		if primitive != nil {
			if handler := primitive.MouseHandler(); handler != nil {
				var wasConsumed bool
				wasConsumed, capturingPrimitive = handler(action, event, func(p Primitive) {
					a.SetFocus(p)
				})
				if wasConsumed {
					consumed = true
				}
			}
		}
		a.mouseCapturingPrimitive = capturingPrimitive
	}

	x, y := event.Position()
	buttons := event.Buttons()
	clickMoved := x != a.mouseDownX || y != a.mouseDownY
	buttonChanges := buttons ^ a.lastMouseButtons

	if x != a.lastMouseX || y != a.lastMouseY {
		fire(MouseMove)
		a.lastMouseX = x
		a.lastMouseY = y
	}

	for _, buttonEvent := range []struct {
		button                  tcell.ButtonMask
		down, up, click, dclick MouseAction
	}{
		{tcell.ButtonPrimary, MouseLeftDown, MouseLeftUp, MouseLeftClick, MouseLeftDoubleClick},
		{tcell.ButtonMiddle, MouseMiddleDown, MouseMiddleUp, MouseMiddleClick, MouseMiddleDoubleClick},
		{tcell.ButtonSecondary, MouseRightDown, MouseRightUp, MouseRightClick, MouseRightDoubleClick},
	} {
		if buttonChanges&buttonEvent.button != 0 {
			if buttons&buttonEvent.button != 0 {
				fire(buttonEvent.down)
			} else {
				fire(buttonEvent.up)
				if !clickMoved {
					if doubleClickInterval == 0 || a.lastMouseClick.Add(doubleClickInterval).Before(time.Now()) {
						fire(buttonEvent.click)
						a.lastMouseClick = time.Now()
					} else {
						fire(buttonEvent.dclick)
						a.lastMouseClick = time.Time{} // reset
					}
				}
			}
		}
	}

	for _, wheelEvent := range []struct {
		button tcell.ButtonMask
		action MouseAction
	}{
		{tcell.WheelUp, MouseScrollUp},
		{tcell.WheelDown, MouseScrollDown},
		{tcell.WheelLeft, MouseScrollLeft},
		{tcell.WheelRight, MouseScrollRight}} {
		if buttons&wheelEvent.button != 0 {
			fire(wheelEvent.action)
		}
	}

	return consumed, isMouseDownAction
}

// Stop stops the application, causing Run() to return.
func (a *Application) Stop() {
	if a.Mach.Not1(ssApp.AppStates.Start) &&
		!a.Mach.WillBe1(ssApp.AppStates.Start) {
		// Run() has not been called yet
		a.finalizeScreen()
		return
	}
	a.Mach.Add1(ssApp.AppStates.CviewStopping, nil)
}

func (a *Application) finalizeScreen() {
	screen := a.getScreen()
	if screen == nil {
		return
	}

	a.setScreen(nil)
	a.finiScreen(screen)
}

// finiScreen finalizes a screen in a separate goroutine, as Fini may deadlock.
func (a *Application) finiScreen(screen tcell.Screen) {
	// TODO deadlocks on SSH
	// screen.Fini()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				// TODO close of a nil chan `quit`
			}
		}()
		screen.Fini()
	}()
}

// Suspend temporarily suspends the application by exiting terminal UI mode and
// invoking the provided function "f". When "f" returns, terminal UI mode is
// entered again and the application resumes.
//
// A return value of true indicates that the application was suspended and "f"
// was called. If false is returned, the application was already suspended,
// terminal UI mode was not exited, and "f" was not called.
func (a *Application) Suspend(f func()) bool {
	done := make(chan bool, 1)
	res := a.Mach.Add1(ssApp.AppStates.CviewSuspending,
		am.Pass(&ASuspend{Fn: f, Done: done}))
	if res == am.Canceled {
		return false
	}

	return <-done
}

// Draw draws the provided primitives on the screen, or when no primitives are
// provided, draws the application's root primitive (i.e. the entire screen).
//
// When one or more primitives are supplied, the Draw functions of the
// primitives are called. Handlers set via BeforeDrawFunc and AfterDrawFunc are
// not called.
//
// When no primitives are provided, the Draw function of the application's root
// primitive is called. This results in drawing the entire screen. Handlers set
// via BeforeDrawFunc and AfterDrawFunc are also called.
func (a *Application) Draw(p ...Primitive) {
	a.QueueUpdate(func() {
		if len(p) == 0 {
			a.draw()
			return
		}

		screen := a.getScreen()
		if screen != nil {
			for _, primitive := range p {
				primitive.Draw(screen)
			}
			screen.Show()
		}
	})
}

// draw actually does what Draw() promises to do.
func (a *Application) draw() {
	screen := a.getScreen()
	root := a.getRoot()
	callbacks := a.getCallbacks()

	// Maybe we're not ready yet or not anymore.
	if screen == nil || root == nil {
		return
	}

	// Resize if requested.
	if a.rootFullscreen.Load() {
		root.SetRect(0, 0, int(a.width.Load()), int(a.height.Load()))
	}

	// Call before handler if there is one.
	if callbacks.beforeDraw != nil {
		if callbacks.beforeDraw(screen) {
			screen.Show()
			return
		}
	}

	// Draw all primitives.
	root.Draw(screen)

	// Call after handler if there is one.
	if callbacks.afterDraw != nil {
		callbacks.afterDraw(screen)
	}

	// Sync screen.
	screen.Show()
}

// SetBeforeDrawFunc installs a callback function which is invoked just before
// the root primitive is drawn during screen updates. If the function returns
// true, drawing will not continue, i.e. the root primitive will not be drawn
// (and an after-draw-handler will not be called).
//
// Note that the screen is not cleared by the application. To clear the screen,
// you may call screen.Clear().
//
// Provide nil to uninstall the callback function.
func (a *Application) SetBeforeDrawFunc(handler func(screen tcell.Screen) bool) {
	a.setCallbacks(func(c *appCallbacks) {
		c.beforeDraw = handler
	})
}

// GetBeforeDrawFunc returns the callback function installed with
// SetBeforeDrawFunc() or nil if none has been installed.
func (a *Application) GetBeforeDrawFunc() func(screen tcell.Screen) bool {
	return a.getCallbacks().beforeDraw
}

// SetAfterDrawFunc installs a callback function which is invoked after the root
// primitive was drawn during screen updates.
//
// Provide nil to uninstall the callback function.
func (a *Application) SetAfterDrawFunc(handler func(screen tcell.Screen)) {
	a.setCallbacks(func(c *appCallbacks) {
		c.afterDraw = handler
	})
}

// GetAfterDrawFunc returns the callback function installed with
// SetAfterDrawFunc() or nil if none has been installed.
func (a *Application) GetAfterDrawFunc() func(screen tcell.Screen) {
	return a.getCallbacks().afterDraw
}

// SetRoot sets the root primitive for this application. If "fullscreen" is set
// to true, the root primitive's position will be changed to fill the screen.
//
// This function must be called at least once or nothing will be displayed when
// the application starts.
//
// It also calls SetFocus() on the primitive and draws the application.
func (a *Application) SetRoot(root Primitive, fullscreen bool) {
	a.setRoot(root)
	a.rootFullscreen.Store(fullscreen)
	if screen := a.getScreen(); screen != nil {
		screen.Clear()
	}

	a.SetFocus(root)

	a.Draw()
}

// ResizeToFullScreen resizes the given primitive such that it fills the entire
// screen.
func (a *Application) ResizeToFullScreen(p Primitive) {
	p.SetRect(0, 0, int(a.width.Load()), int(a.height.Load()))
}

// SetAfterResizeFunc installs a callback function which is invoked when the
// application's window is initialized, and when the application's window size
// changes. After invoking this callback the screen is cleared and the
// application is drawn.
//
// Provide nil to uninstall the callback function.
func (a *Application) SetAfterResizeFunc(handler func(width int, height int)) {
	a.setCallbacks(func(c *appCallbacks) {
		c.afterResize = handler
	})
}

// GetAfterResizeFunc returns the callback function installed with
// SetAfterResizeFunc() or nil if none has been installed.
func (a *Application) GetAfterResizeFunc() func(width int, height int) {
	return a.getCallbacks().afterResize
}

// SetFocus sets the focus on a new primitive. All key events will be redirected
// to that primitive. Callers must ensure that the primitive will handle key
// events.
//
// Blur() will be called on the previously focused primitive. Focus() will be
// called on the new primitive.
func (a *Application) SetFocus(p Primitive) {
	callbacks := a.getCallbacks()

	if callbacks.beforeFocus != nil {
		if !callbacks.beforeFocus(p) {
			return
		}
	}

	if focus := a.getFocus(); focus != nil {
		focus.Blur()
	}

	a.setFocus(p)

	if screen := a.getScreen(); screen != nil {
		screen.HideCursor()
	}

	if callbacks.afterFocus != nil {
		callbacks.afterFocus(p)
	}

	if p != nil {
		p.Focus(func(p Primitive) {
			a.SetFocus(p)
		})
	}
}

// GetFocus returns the primitive which has the current focus. If none has it,
// nil is returned.
func (a *Application) GetFocus() Primitive {
	return a.getFocus()
}

// SetBeforeFocusFunc installs a callback function which is invoked before the
// application's focus changes. Return false to maintain the current focus.
//
// Provide nil to uninstall the callback function.
func (a *Application) SetBeforeFocusFunc(handler func(p Primitive) bool) {
	a.setCallbacks(func(c *appCallbacks) {
		c.beforeFocus = handler
	})
}

// SetAfterFocusFunc installs a callback function which is invoked after the
// application's focus changes.
//
// Provide nil to uninstall the callback function.
func (a *Application) SetAfterFocusFunc(handler func(p Primitive)) {
	a.setCallbacks(func(c *appCallbacks) {
		c.afterFocus = handler
	})
}

// QueueUpdate queues a function to be executed as part of the event loop.
//
// Note that Draw() is not implicitly called after the execution of f as that
// may not be desirable. You can call Draw() from f if the screen should be
// refreshed after each update. Alternatively, use QueueUpdateDraw() to follow
// up with an immediate refresh of the screen.
func (a *Application) QueueUpdate(f func()) {
	a.updates <- f
}

// QueueUpdateDraw works like QueueUpdate() except, when one or more primitives
// are provided, the primitives are drawn after the provided function returns.
// When no primitives are provided, the entire screen is drawn after the
// provided function returns.
func (a *Application) QueueUpdateDraw(f func(), p ...Primitive) {
	a.QueueUpdate(func() {
		f()

		if len(p) == 0 {
			a.draw()
			return
		}
		screen := a.getScreen()
		if screen != nil {
			for _, primitive := range p {
				primitive.Draw(screen)
			}
			screen.Show()
		}
	})
}

// QueueEvent sends an event to the Application event loop.
//
// It is not recommended for event to be nil.
func (a *Application) QueueEvent(event tcell.Event) {
	a.events <- event
}

// RingBell sends a bell code to the terminal.
func (a *Application) RingBell() {
	a.QueueUpdate(func() {
		fmt.Print(string(byte(7)))
	})
}

// ScreenText extracts the current screen buffer as plain text.
func (a *Application) ScreenText() string {
	return TextFromScreen(a.getScreen())
}

func TextFromScreen(screen tcell.Screen) string {
	if screen == nil {
		return ""
	}

	width, height := screen.Size()
	var builder strings.Builder

	// Pre-allocate memory based on screen size (width + 1 for newline) * height
	builder.Grow((width + 1) * height)

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			// GetContent returns the rune, combining runes, style, and cell width.
			// We ignore the style completely, which strips all colors/formatting.
			str, _, strWidth := screen.Get(x, y)
			builder.Write([]byte(str))

			// If a character spans multiple cells (e.g., CJK characters or emojis),
			// skip the next cell(s) to avoid duplicating characters or reading placeholders.
			if strWidth > 1 {
				x += strWidth - 1
			}
		}
		// Append a newline at the end of each row
		builder.WriteRune('\n')
	}

	return builder.String()
}

// ///// ///// /////

// ///// EVENT LOOP

// ///// ///// /////

// handle processes a single tcell event. It must be called on the machine's
// queue, i.e. from a handler or from [am.Machine.Eval].
func (a *Application) handle(event interface{}) {
	focus := a.getFocus()
	callbacks := a.getCallbacks()
	screen := a.getScreen()

	switch event := event.(type) {
	case *tcell.EventKey:
		// Intercept keys.
		if callbacks.inputCapture != nil {
			event = callbacks.inputCapture(event)
			if event == nil {
				if callbacks.drawOnKey {
					a.draw()
				}
				return // Don't forward event.
			}
		}

		// Ctrl-C closes the application.
		if event.Key() == tcell.KeyCtrlC {
			a.Stop()
			return
		}

		// Pass other key events to the currently focused primitive.
		if focus != nil {
			if handler := focus.InputHandler(); handler != nil {
				handler(event, func(p Primitive) {
					a.SetFocus(p)
				})
				if callbacks.drawOnKey {
					a.draw()
				}
			}
		}
	case *tcell.EventResize:
		// Throttle resize events.
		if time.Since(a.lastResize) < resizeEventThrottle {
			// Stop timer
			if a.throttleResize != nil && !a.throttleResize.Stop() {
				select {
				case <-a.throttleResize.C:
				default:
				}
			}

			event := event // Capture

			// Start timer
			a.throttleResize = time.AfterFunc(resizeEventThrottle, func() {
				a.events <- event
			})

			return
		}

		a.lastResize = time.Now()

		if screen == nil {
			return
		}

		screen.Clear()
		width, height := event.Size()
		a.width.Store(int32(width))
		a.height.Store(int32(height))

		// Call afterResize handler if there is one.
		if callbacks.afterResize != nil {
			callbacks.afterResize(width, height)
		}

		a.draw()
	case *tcell.EventMouse:
		consumed, isMouseDownAction := a.fireMouseActions(event)
		if consumed {
			a.draw()
		}
		a.lastMouseButtons = event.Buttons()
		if isMouseDownAction {
			a.mouseDownX, a.mouseDownY = event.Position()
		}
	}
}

// startPolling starts a screen event loop for the passed screen. It exits when
// the screen gets replaced or finalized.
func (a *Application) startPolling(screen tcell.Screen) {
	ctx := a.Mach.NewStateCtx(ssApp.AppStates.Start)

	a.Mach.Go(ctx, func() {
		for {
			event := screen.PollEvent()
			if a.getScreen() != screen {
				// screen replaced or finalized
				return
			}
			if event == nil {
				a.Mach.Add1(ssApp.AppStates.CviewStopping, nil)
				return
			}

			ok := a.Mach.Eval("pollEvent", func() {
				defer a.HandlePanic()
				a.handle(event)
			}, ctx)
			if !ok && ctx.Err() != nil {
				return // expired
			}
		}
	})
}

// updatesLoop consumes queued updates and executes them on the machine's queue.
func (a *Application) updatesLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case update := <-a.updates:
			ok := a.Mach.Eval("queueUpdate", func() {
				defer a.HandlePanic()
				update()
			}, ctx)
			if !ok && ctx.Err() != nil {
				return // expired
			}
		}
	}
}

// eventsLoop consumes queued tcell events and executes them on the machine's
// queue.
func (a *Application) eventsLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-a.events:
			ok := a.Mach.Eval("queueEvent", func() {
				defer a.HandlePanic()
				a.handle(event)
			}, ctx)
			if !ok && ctx.Err() != nil {
				return // expired
			}
		}
	}
}

// ///// ///// /////

// ///// FIELDS

// ///// ///// /////

func (a *Application) getScreen() tcell.Screen {
	ptr := a.screen.Load()
	if ptr == nil {
		return nil
	}
	return *ptr
}

func (a *Application) setScreen(screen tcell.Screen) {
	if screen == nil {
		a.screen.Store(nil)
		return
	}
	a.screen.Store(&screen)
}

func (a *Application) getFocus() Primitive {
	ptr := a.focus.Load()
	if ptr == nil {
		return nil
	}
	return ptr.p
}

func (a *Application) setFocus(p Primitive) {
	if p == nil {
		a.focus.Store(nil)
		return
	}
	a.focus.Store(&primitiveRef{p: p})
}

func (a *Application) getRoot() Primitive {
	ptr := a.root.Load()
	if ptr == nil {
		return nil
	}
	return ptr.p
}

func (a *Application) setRoot(p Primitive) {
	if p == nil {
		a.root.Store(nil)
		return
	}
	a.root.Store(&primitiveRef{p: p})
}

func (a *Application) getCallbacks() *appCallbacks {
	if c := a.callbacks.Load(); c != nil {
		return c
	}
	return &appCallbacks{drawOnKey: true}
}

func (a *Application) setCallbacks(fn func(c *appCallbacks)) {
	callbacks := *a.getCallbacks()
	fn(&callbacks)
	a.callbacks.Store(&callbacks)
}

// ///// ///// /////

// ///// HANDLERS

// ///// ///// /////

// appHandlers contains the event loop handlers of the Application state
// machine.
type appHandlers struct {
	*am.ExceptionHandler

	app *Application
}

var _ = ssApp.AppStates.Start
var _ = ssApp.AppStates.CviewStopped

func (h *appHandlers) StartState(e *am.Event) {
	a := h.app

	// initialize the screen
	if err := a.init(); err != nil {
		a.runErr = err
		a.Mach.EvAddErrState(e, ssApp.AppStates.CviewErrScreen, err, nil)
		a.Mach.EvAdd1(e, ssApp.AppStates.CviewStopping, nil)
		return
	}

	// the queue consumers live as long as the machine does
	ctx := a.Mach.Context()
	a.Mach.Go(ctx, func() {
		a.updatesLoop(ctx)
	})
	a.Mach.Go(ctx, func() {
		a.eventsLoop(ctx)
	})

	a.Mach.EvAdd1(e, ssApp.AppStates.CviewScreenInitialized, nil)
}

func (h *appHandlers) CviewScreenInitializedState(e *am.Event) {
	a := h.app

	// draw the screen for the first time
	a.draw()

	// start the input event loop
	a.startPolling(a.getScreen())

	a.Mach.EvAdd1(e, ssApp.AppStates.CviewRunning, nil)
}

func (h *appHandlers) CviewScreenReplacingState(e *am.Event) {
	a := h.app
	args := am.ParseArgs[AScreen](e.Args)
	if args == nil || args.Screen == nil {
		return
	}

	if old := a.getScreen(); old != nil {
		a.finiScreen(old)
	}
	a.setScreen(args.Screen)
	if err := args.Screen.Init(); err != nil {
		a.Mach.EvAddErrState(e, ssApp.AppStates.CviewErrScreen, err, nil)
		return
	}
	if a.enableBracketedPaste.Load() {
		args.Screen.EnablePaste()
	}
	if a.enableMouse.Load() {
		args.Screen.EnableMouse()
	}

	// start a new input event loop
	a.startPolling(args.Screen)

	a.draw()
}

func (h *appHandlers) CviewSuspendingState(e *am.Event) {
	a := h.app
	args := am.ParseArgs[ASuspend](e.Args)
	if args == nil {
		return
	}

	screen := a.getScreen()
	if screen == nil {
		args.Done <- false
		return
	}
	if err := screen.Suspend(); err != nil {
		args.Done <- false
		a.Mach.EvAddErrState(e, ssApp.AppStates.CviewErrScreen, err, nil)
		return
	}

	// resume once the suspended func returns
	ctx := a.Mach.NewStateCtx(ssApp.AppStates.Start, e)
	a.Mach.Go(ctx, func() {
		args.Fn()
		a.Mach.Add1(ssApp.AppStates.CviewResuming, am.Pass(args))
	})
}

func (h *appHandlers) CviewResumingState(e *am.Event) {
	a := h.app
	args := am.ParseArgs[ASuspend](e.Args)
	if args == nil {
		return
	}

	screen := a.getScreen()
	if screen == nil {
		args.Done <- true
		return
	}
	if err := screen.Resume(); err != nil {
		a.Mach.EvAddErrState(e, ssApp.AppStates.CviewErrScreen, err, nil)
	}
	args.Done <- true
}

func (h *appHandlers) CviewStoppingState(e *am.Event) {
	a := h.app

	a.finalizeScreen()

	a.Mach.EvAdd1(e, ssApp.AppStates.CviewStopped, nil)
}
