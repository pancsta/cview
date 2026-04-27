package main

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/pancsta/cview"
)

// End shows the final slide.
func End(nextSlide func()) (title string, info string, content cview.Primitive) {
	textView := cview.NewTextView()
	textView.SetDoneFunc(func(key tcell.Key) {
		nextSlide()
	})
	url := "https://github.com/pancsta/cview"
	fmt.Fprint(textView, url)
	return "End", "", Center(len(url), 1, textView)
}
