// Package states contains state definitions for the cview Application state
// machine.
package states

import (
	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	ss "github.com/pancsta/asyncmachine-go/pkg/states"
	. "github.com/pancsta/asyncmachine-go/pkg/states/global"
)

// ///// ///// /////

// ///// APP

// ///// ///// /////

// CviewStatesDef contains all the states defined by cview.
type CviewStatesDef struct {
	*am.StatesBase

	// CviewErrScreen indicates the screen couldn't be initialized, replaced, or
	// suspended.
	CviewErrScreen string

	// CviewScreenInitialized indicates the tcell screen has been initialized.
	CviewScreenInitialized string
	// CviewRunning indicates the event loop is polling for input events.
	CviewRunning string
	// CviewScreenReplacing indicates a screen replacement is in progress.
	CviewScreenReplacing string
	// CviewSuspending indicates the application is leaving terminal UI mode.
	CviewSuspending string
	// CviewResuming indicates the application is re-entering terminal UI mode.
	CviewResuming string
	// CviewStopping indicates the application is shutting down.
	CviewStopping string
	// CviewStopped indicates the application has stopped and Run will return.
	CviewStopped string
}

// AppStatesDef contains all the states of the Application state machine.
type AppStatesDef struct {
	*am.StatesBase

	*CviewStatesDef

	// inherit from BasicStatesDef
	*ss.BasicStatesDef
}

// CviewSchema represents all relations and properties of CviewStates.
var CviewSchema = am.Schema{

	// Errors

	ssA.CviewErrScreen: {
		Multi:   true,
		Add:     S{Exception},
		Require: S{Exception},
	},

	// Lifecycle

	ssA.CviewScreenInitialized: {
		Require: S{ssA.Start},
	},
	ssA.CviewRunning: {
		Require: S{ssA.CviewScreenInitialized},
	},
	ssA.CviewScreenReplacing: {
		Require: S{ssA.Start},
		Multi:   true,
	},
	ssA.CviewSuspending: {
		Require: S{ssA.Start},
	},
	ssA.CviewResuming: {
		Require: S{ssA.Start},
	},
	ssA.CviewStopping: {
		Remove: S{ssA.Start, ssA.CviewScreenInitialized, ssA.CviewRunning,
			ssA.CviewScreenReplacing, ssA.CviewSuspending},
	},
	ssA.CviewStopped: {
		Remove: S{ssA.Start},
	},
}

// AppSchema represents all relations and properties of AppStates.
var AppSchema = ss.BasicSchema.Merge(CviewSchema)

// EXPORTS

var (
	ssA = am.NewStates(AppStatesDef{})

	// AppStates contains all the states of the Application state machine.
	AppStates = ssA
)
