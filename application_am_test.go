package cview

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"

	ssApp "github.com/pancsta/cview/states"
)

// TestApplicationRun verifies the asyncmachine-go driven event loop.
func TestApplicationRun(t *testing.T) {
	app := NewApplication()
	sc := tcell.NewSimulationScreen("UTF-8")
	if err := sc.Init(); err != nil {
		t.Fatal(err)
	}
	sc.SetSize(40, 10)
	app.SetScreen(sc)

	tv := NewTextView()
	tv.SetText("hello")
	app.SetRoot(tv, true)

	runErr := make(chan error, 1)
	go func() {
		runErr <- app.Run()
	}()

	// wait until the event loop is running
	<-app.Mach.When1(ssApp.AppStates.CviewRunning, nil)

	// queue an update and redraw
	app.QueueUpdateDraw(func() {
		tv.SetText("hello asyncmachine")
	})

	// wait for the update to be drawn
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(app.ScreenText(), "hello asyncmachine") {
		if time.Now().After(deadline) {
			t.Fatalf("update was not drawn, screen:\n%s", app.ScreenText())
		}
		time.Sleep(10 * time.Millisecond)
	}

	// stop the app
	go app.Stop()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run() returned an error: %s", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return after Stop()")
	}

	if app.Mach.Is1(ssApp.AppStates.Start) {
		t.Error("Start should be inactive after Run() returns")
	}
}

// TestApplicationQueueEvent verifies that queued events are handled.
func TestApplicationQueueEvent(t *testing.T) {
	app := NewApplication()
	sc := tcell.NewSimulationScreen("UTF-8")
	if err := sc.Init(); err != nil {
		t.Fatal(err)
	}
	sc.SetSize(40, 10)
	app.SetScreen(sc)

	tv := NewTextView()
	tv.SetText("initial")
	app.SetRoot(tv, true)

	runErr := make(chan error, 1)
	go func() {
		runErr <- app.Run()
	}()
	<-app.Mach.When1(ssApp.AppStates.CviewRunning, nil)

	// queue a key event
	app.QueueEvent(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))

	// stop and wait
	go app.Stop()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run() returned an error: %s", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return after Stop()")
	}
}

// TestApplicationSetScreen verifies screen replacement during Run.
func TestApplicationSetScreen(t *testing.T) {
	app := NewApplication()
	scA := tcell.NewSimulationScreen("UTF-8")
	if err := scA.Init(); err != nil {
		t.Fatal(err)
	}
	scA.SetSize(40, 10)
	app.SetScreen(scA)

	tv := NewTextView()
	tv.SetText("screen A")
	app.SetRoot(tv, true)

	runErr := make(chan error, 1)
	go func() {
		runErr <- app.Run()
	}()
	<-app.Mach.When1(ssApp.AppStates.CviewRunning, nil)

	// replace the screen
	scB := tcell.NewSimulationScreen("UTF-8")
	scB.SetSize(20, 5)
	app.SetScreen(scB)

	deadline := time.Now().Add(5 * time.Second)
	for app.GetScreen() != scB {
		if time.Now().After(deadline) {
			t.Fatal("screen was not replaced")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// wait for the first draw on the new screen
	deadline = time.Now().Add(5 * time.Second)
	for !strings.Contains(app.ScreenText(), "screen A") {
		if time.Now().After(deadline) {
			t.Fatalf("new screen was not drawn, screen:\n%s", app.ScreenText())
		}
		time.Sleep(10 * time.Millisecond)
	}

	go app.Stop()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run() returned an error: %s", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return after Stop()")
	}
}

// TestApplicationSuspend verifies suspending and resuming the application.
func TestApplicationSuspend(t *testing.T) {
	app := NewApplication()
	sc := tcell.NewSimulationScreen("UTF-8")
	if err := sc.Init(); err != nil {
		t.Fatal(err)
	}
	sc.SetSize(40, 10)
	app.SetScreen(sc)

	tv := NewTextView()
	tv.SetText("suspend")
	app.SetRoot(tv, true)

	runErr := make(chan error, 1)
	go func() {
		runErr <- app.Run()
	}()
	<-app.Mach.When1(ssApp.AppStates.CviewRunning, nil)

	var suspended bool
	ok := make(chan bool, 1)
	go func() {
		ok <- app.Suspend(func() {
			suspended = true
		})
	}()
	select {
	case res := <-ok:
		if !res {
			t.Error("Suspend() returned false")
		}
		if !suspended {
			t.Error("Suspend() did not call f")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Suspend() did not return")
	}

	go app.Stop()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run() returned an error: %s", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return after Stop()")
	}
}

// TestNewApplicationWithMach verifies injecting an existing machine.
func TestNewApplicationWithMach(t *testing.T) {
	schema := ssApp.AppSchema.Merge(am.Schema{
		"Custom": {},
	})
	names := ssApp.AppStates.Names().Add1("Custom")

	mach, err := am.NewCommon(context.Background(), "cview-external", schema,
		names, nil, nil, &am.Opts{HandlerTimeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	mach.EvalTimeout = 30 * time.Second

	app, err := NewApplicationWithMach(mach)
	if err != nil {
		t.Fatal(err)
	}
	if app.Mach != mach {
		t.Fatal("application did not use the passed machine")
	}

	sc := tcell.NewSimulationScreen("UTF-8")
	if err := sc.Init(); err != nil {
		t.Fatal(err)
	}
	sc.SetSize(40, 10)
	app.SetScreen(sc)

	tv := NewTextView()
	tv.SetText("external machine")
	app.SetRoot(tv, true)

	runErr := make(chan error, 1)
	go func() {
		runErr <- app.Run()
	}()
	<-app.Mach.When1(ssApp.AppStates.CviewRunning, nil)

	go app.Stop()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run() returned an error: %s", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return after Stop()")
	}
}

// TestNewApplicationWithMachInvalid verifies schema validation.
func TestNewApplicationWithMachInvalid(t *testing.T) {
	schema := am.Schema{
		am.StateException: {},
		"Foo":             {},
	}
	mach, err := am.NewCommon(context.Background(), "cview-invalid", schema,
		am.S{am.StateException, "Foo"}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := NewApplicationWithMach(mach); err == nil {
		t.Fatal("expected an error for a schema without app states")
	}
}

// TestNewApplicationWithMachNil verifies the nil fallback.
func TestNewApplicationWithMachNil(t *testing.T) {
	app, err := NewApplicationWithMach(nil)
	if err != nil {
		t.Fatal(err)
	}
	if app.Mach == nil {
		t.Fatal("application has no machine")
	}
}
