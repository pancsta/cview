// Demo code for the Flex primitive.
package main

import (
	"strconv"

	"github.com/pancsta/cview"
)

func demoBox(title string) *cview.Box {
	b := cview.NewBox()
	b.SetBorder(true)
	b.SetTitle(title)
	return b
}

func main() {
	app := cview.NewApplication()
	defer app.HandlePanic()

	app.EnableMouse(true)

	subFlex := cview.NewFlex()

	scroll := cview.NewScrollView()
	scroll.SetScrollBarVisibility(cview.ScrollBarAlways)
	for i := 0; i < 15; i++ {
		scroll.AddItem(demoBox("Box "+strconv.Itoa(i)), 3, false)
	}
	scroll.ScrollTo(5, 0)

	flex := cview.NewFlex()
	flex.SetDirection(cview.FlexRow)
	flex.AddItem(subFlex, 0, 1, false)
	flex.AddItem(scroll, 0, 3, false)

	app.SetRoot(flex, true)
	if err := app.Run(); err != nil {
		panic(err)
	}
}
