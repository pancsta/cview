package cview

import (
	"sync"

	"github.com/gdamore/tcell/v2"
)

// Configuration values.
const (
	ScrollRow = iota
	ScrollColumn
)

// scrollItem holds layout options for one item.
type scrollItem struct {
	Item      Primitive // The item to be positioned. May be nil for an empty item.
	FixedSize int       // The item's fixed size which may not be changed, 0 if it has no fixed size.
	Focus     bool      // Whether or not this item attracts the layout's focus.
}

// ScrollView is a basic implementation of the Scrollbox layout. The contained
// primitives are arranged horizontally or vertically. The way they are
// distributed along that dimension depends on their layout settings, which is
// either a fixed length or a proportional length. See AddItem() for details.
type ScrollView struct {
	*Box

	// The items to be positioned.
	items []*scrollItem

	// // ScrollRow or ScrollColumn.
	// direction int

	// If set to true, ScrollView will use the entire screen as its available space
	// instead its box dimensions.
	fullScreen bool

	// Visibility of the scroll bar.
	scrollBarVisibility ScrollBarVisibility

	// The scroll bar color.
	scrollBarColor tcell.Color

	// The number of characters to be skipped on each line (not in wrap mode).
	heightOffset int

	sync.RWMutex
}

// NewScrollView returns a new scrollbox layout container with no primitives and its
// direction set to ScrollColumn. To add primitives to this layout, see AddItem().
// To change the direction, see SetDirection().
//
// Note that ScrollView will have a transparent background by default so that any nil
// scroll items will show primitives behind the ScrollView.
// To disable this transparency:
//
//	scroll.SetBackgroundTransparent(false)
func NewScrollView() *ScrollView {
	f := &ScrollView{
		Box:                 NewBox(),
		scrollBarVisibility: ScrollBarAuto,
		scrollBarColor:      Styles.ScrollBarColor,
	}
	f.SetBackgroundTransparent(true)
	f.focus = f
	return f
}

// SetScrollBarVisibility specifies the display of the scroll bar.
func (f *ScrollView) SetScrollBarVisibility(visibility ScrollBarVisibility) {
	f.Lock()
	defer f.Unlock()

	f.scrollBarVisibility = visibility
}

// SetScrollBarColor sets the color of the scroll bar.
func (f *ScrollView) SetScrollBarColor(color tcell.Color) {
	f.Lock()
	defer f.Unlock()

	f.scrollBarColor = color
}

// SetFullScreen sets the flag which, when true, causes the scroll layout to use
// the entire screen space instead of whatever size it is currently assigned to.
func (f *ScrollView) SetFullScreen(fullScreen bool) {
	f.Lock()
	defer f.Unlock()

	f.fullScreen = fullScreen
}

// GetItems returns a slice of all items in the container.
func (f *ScrollView) GetItems() []Primitive {
	f.RLock()
	defer f.RUnlock()
	var ret []Primitive
	for _, item := range f.items {
		ret = append(ret, item.Item)
	}

	return ret
}

// AddItem adds a new item to the container. The "fixedSize" argument is a width
// or height that may not be changed by the layout algorithm. A value of 0 means
// that its size is scrollible and may be changed. The "proportion" argument
// defines the relative size of the item compared to other scrollible-size items.
// For example, items with a proportion of 2 will be twice as large as items
// with a proportion of 1. The proportion must be at least 1 if fixedSize == 0
// (ignored otherwise).
//
// If "focus" is set to true, the item will receive focus when the ScrollView
// primitive receives focus. If multiple items have the "focus" flag set to
// true, the first one will receive focus.
//
// A nil value for the primitive represents empty space.
func (f *ScrollView) AddItem(item Primitive, fixedSize int, focus bool) {
	f.Lock()
	defer f.Unlock()

	if item == nil {
		item = NewBox()
		item.SetVisible(false)
	}

	f.items = append(f.items, &scrollItem{Item: item, FixedSize: fixedSize, Focus: focus})
}

// AddItemAtIndex adds an item to the scroll at a given index.
// For more information see AddItem.
func (f *ScrollView) AddItemAtIndex(index int, item Primitive, fixedSize int, focus bool) {
	f.Lock()
	defer f.Unlock()
	newItem := &scrollItem{Item: item, FixedSize: fixedSize, Focus: focus}

	if index == 0 {
		f.items = append([]*scrollItem{newItem}, f.items...)
	} else {
		f.items = append(f.items[:index], append([]*scrollItem{newItem}, f.items[index:]...)...)
	}
}

// RemoveItem removes all items for the given primitive from the container,
// keeping the order of the remaining items intact.
func (f *ScrollView) RemoveItem(p Primitive) {
	f.Lock()
	defer f.Unlock()

	for index := len(f.items) - 1; index >= 0; index-- {
		if f.items[index].Item == p {
			f.items = append(f.items[:index], f.items[index+1:]...)
		}
	}
}

// ResizeItem sets a new size for the item(s) with the given primitive. If there
// are multiple ScrollView items with the same primitive, they will all receive the
// same size. For details regarding the size parameters, see AddItem().
func (f *ScrollView) ResizeItem(p Primitive, fixedSize int) {
	f.Lock()
	defer f.Unlock()

	for _, item := range f.items {
		if item.Item == p {
			item.FixedSize = fixedSize
		}
	}
}

// Draw draws this primitive onto the screen.
func (f *ScrollView) Draw(screen tcell.Screen) {
	if !f.GetVisible() {
		return
	}

	f.Box.Draw(screen)

	f.Lock()
	defer f.Unlock()

	// Calculate size and position of the items.

	// Do we use the entire screen?
	if f.fullScreen {
		width, height := screen.Size()
		f.SetRect(0, 0, width, height)
	}
	// How much space can we distribute?
	x, y, width, visibleheight := f.GetInnerRect()

	// How tall is the content?
	contentHeight := 0
	for _, item := range f.items {
		contentHeight += item.FixedSize
	}
	if contentHeight > visibleheight && y+f.heightOffset+visibleheight > y+contentHeight {
		f.heightOffset = contentHeight - visibleheight
	}

	showVerticalScrollBar := f.scrollBarVisibility == ScrollBarAlways || (f.scrollBarVisibility == ScrollBarAuto && contentHeight > visibleheight)
	if width > 0 && showVerticalScrollBar {
		width-- // Subtract space for scroll bar.
	}

	// draw
	pos := y
	posScrolled := y
	firstVisibleY := -1

	// TODO scroll to focused one when not visible
	for _, item := range f.items {
		size := item.FixedSize

		// scrolled
		if posScrolled < y+f.heightOffset && showVerticalScrollBar {
			posScrolled += size
			continue
		}
		if firstVisibleY == -1 {
			firstVisibleY = posScrolled
		}
		posScrolled += size

		item.Item.SetRect(x, pos, width, size)
		pos += size

		if item.Item != nil {
			if item.Item.GetFocusable().HasFocus() {
				defer item.Item.Draw(screen)
			} else {
				item.Item.Draw(screen)
			}
		}
	}

	// fill the remaining space
	if pos < y+visibleheight {
		for i := pos; i < y+visibleheight; i++ {
			for xx := 0; x+xx < width; xx++ {
				screen.SetContent(x, i, ' ', nil, tcell.StyleDefault.Background(Styles.PrimitiveBackgroundColor))
			}
		}
	}

	if !showVerticalScrollBar {
		return
	}

	cursor := int(float64(contentHeight) * (float64(firstVisibleY-y) / float64(contentHeight-visibleheight)))
	if cursor > contentHeight {
		cursor = contentHeight
	}

	for printed := 0; printed < visibleheight; printed++ {
		RenderScrollBar(screen, f.scrollBarVisibility, x+width, y+printed, visibleheight, contentHeight, cursor, printed, f.hasFocus, f.scrollBarColor)
	}
}

// ScrollTo scrolls to the specified height and width (both starting with 0).
func (f *ScrollView) ScrollTo(height, width int) {
	f.Lock()
	defer f.Unlock()

	f.heightOffset = height
	// t.columnOffset = column
	// t.trackEnd = false
}

// Focus is called when this primitive receives focus.
func (f *ScrollView) Focus(delegate func(p Primitive)) {
	f.Lock()

	for _, item := range f.items {
		if item.Item != nil && item.Focus {
			f.Unlock()
			delegate(item.Item)
			return
		}
	}

	f.Unlock()
}

// HasFocus returns whether or not this primitive has focus.
func (f *ScrollView) HasFocus() bool {
	f.RLock()
	defer f.RUnlock()

	for _, item := range f.items {
		if item.Item != nil && item.Item.GetFocusable().HasFocus() {
			return true
		}
	}
	return false
}

// MouseHandler returns the mouse handler for this primitive.
func (f *ScrollView) MouseHandler() func(action MouseAction, event *tcell.EventMouse, setFocus func(p Primitive)) (consumed bool, capture Primitive) {
	return f.WrapMouseHandler(func(action MouseAction, event *tcell.EventMouse, setFocus func(p Primitive)) (consumed bool, capture Primitive) {
		if !f.InRect(event.Position()) {
			return false, nil
		}

		switch action {
		case MouseScrollUp:
			f.heightOffset--
			if f.heightOffset < 0 {
				f.heightOffset = 0
			}
			consumed = true
		case MouseScrollDown:
			f.heightOffset++
			consumed = true
		}

		if consumed {
			return
		}

		// Pass mouse events along to the first child item that takes it.
		for _, item := range f.items {
			if item.Item == nil {
				continue
			}

			consumed, capture = item.Item.MouseHandler()(action, event, setFocus)
			if consumed {
				return
			}
		}

		return
	})
}
