package cview

import (
	"github.com/gdamore/tcell/v2"
	am "github.com/pancsta/asyncmachine-go/pkg/machine"

	ssApp "github.com/pancsta/cview/states"
)

const APrefix = "cview"

// Args is shared pkg args for the cview states.
type Args struct {
	am.ArgsBase `json:"-"`
}

func (Args) ArgsPrefix() string {
	return APrefix
}

// -----

// AScreen is passed to the CviewScreenReplacing state.
type AScreen struct {
	Args `json:"-"`

	// Screen to replace the current screen with.
	Screen tcell.Screen `log:"screen"`
}

func (AScreen) ArgsState() string {
	return ssApp.AppStates.CviewScreenReplacing
}

// -----

// ASuspend is passed to the CviewSuspending state.
type ASuspend struct {
	Args `json:"-"`

	// Fn is the function to call while the screen is suspended.
	Fn func()
	// Done receives the result of the suspension.
	Done chan bool
}

func (ASuspend) ArgsState() string {
	return ssApp.AppStates.CviewSuspending
}
