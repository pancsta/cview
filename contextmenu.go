package cview

// ContextMenu is a menu that appears upon user interaction, such as right
// clicking or pressing Alt+Enter.
type ContextMenu struct {
	parent   Primitive
	item     int
	open     bool
	drag     bool
	list     *List
	x, y     int
	selected func(int, string, rune)
}

// NewContextMenu returns a new context menu.
func NewContextMenu(parent Primitive) *ContextMenu {
	return &ContextMenu{
		parent: parent,
	}
}

func (c *ContextMenu) initializeList() {
	if c.list != nil {
		return
	}

	c.list = NewList()
	c.list.ShowSecondaryText(false)
	c.list.SetHover(true)
	c.list.SetWrapAround(true)
	c.list.ShowFocus(false)
	c.list.SetBorder(true)
	c.list.SetPadding(
		Styles.ContextMenuPaddingTop,
		Styles.ContextMenuPaddingBottom,
		Styles.ContextMenuPaddingLeft,
		Styles.ContextMenuPaddingRight)
}

// ContextMenuList returns the underlying List of the context menu.
func (c *ContextMenu) ContextMenuList() *List {
	c.initializeList()

	return c.list
}

// AddContextItem adds an item to the context menu. Adding an item with no text
// or shortcut will add a divider.
func (c *ContextMenu) AddContextItem(text string, shortcut rune, selected func(index int)) {
	c.initializeList()

	item := NewListItem(text)
	item.SetShortcut(shortcut)
	item.SetSelectedFunc(c.wrap(selected))

	c.list.AddItem(item)
	if text == "" && shortcut == 0 {
		index := len(c.list.items) - 1
		c.list.items[index].disabled = true
	}
}

func (c *ContextMenu) wrap(f func(index int)) func() {
	return func() {
		f(c.item)
	}
}

// ClearContextMenu removes all items from the context menu.
func (c *ContextMenu) ClearContextMenu() {
	c.initializeList()

	c.list.Clear()
}

// SetContextSelectedFunc sets the function which is called when the user
// selects a context menu item. The function receives the item's index in the
// menu (starting with 0), its text and its shortcut rune. SetSelectedFunc must
// be called before the context menu is shown.
func (c *ContextMenu) SetContextSelectedFunc(handler func(index int, text string, shortcut rune)) {
	c.selected = handler
}

// ShowContextMenu shows the context menu. Provide -1 for both to position on
// the selected item, or specify a 	position.
func (c *ContextMenu) ShowContextMenu(item int, x int, y int, setFocus func(Primitive)) {
	c.show(item, x, y, setFocus)
}

// HideContextMenu hides the context menu.
func (c *ContextMenu) HideContextMenu(setFocus func(Primitive)) {
	c.hide(setFocus)
}

// ContextMenuVisible returns whether or not the context menu is visible.
func (c *ContextMenu) ContextMenuVisible() bool {
	return c.open
}

func (c *ContextMenu) show(item int, x int, y int, setFocus func(Primitive)) {
	c.initializeList()

	if len(c.list.items) == 0 {
		return
	}

	c.open = true
	c.item = item
	c.x, c.y = x, y

	for i, item := range c.list.items {
		if !item.disabled {
			c.list.currentItem = i
			break
		}
	}

	c.list.SetSelectedFunc(func(index int, item *ListItem) {
		// A context item was selected. Close the menu.
		c.hide(setFocus)

		if c.selected != nil {
			c.selected(index, string(item.mainText), item.shortcut)
		}
	})
	c.list.SetDoneFunc(func() {
		c.hide(setFocus)
	})

	setFocus(c.list)
}

func (c *ContextMenu) hide(setFocus func(Primitive)) {
	c.initializeList()

	c.open = false

	if c.list.HasFocus() {
		setFocus(c.parent)
	}
}
