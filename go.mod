module github.com/pancsta/cview

go 1.24.0

toolchain go1.24.5

//replace github.com/gdamore/tcell/v2 => ../tcell-v2
//replace github.com/pancsta/tcell-v2 => ../tcell-v2

require (
	github.com/lucasb-eyer/go-colorful v1.3.0
	github.com/mattn/go-runewidth v0.0.16
	github.com/pancsta/tcell-v2 v0.0.1-fork1
	github.com/rivo/uniseg v0.4.7
)

require (
	github.com/gdamore/encoding v1.0.1 // indirect
	golang.org/x/sys v0.38.0 // indirect
	golang.org/x/term v0.37.0 // indirect
	golang.org/x/text v0.31.0 // indirect
)
