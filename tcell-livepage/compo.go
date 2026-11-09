package tcelllivepage

import (
	"strconv"

	am "github.com/pancsta/asyncmachine-go/pkg/machine"
	h "github.com/pancsta/go-app/pkg/app"
	lp "github.com/pancsta/livepage"
)

// compo renders the tcell grid as a livepage component.
type compo struct {
	lp.Compo
	s *Screen
}

var _ lp.CompoLiveApi = (*compo)(nil)

// Mach implements [lp.CompoLiveApi].
func (c *compo) Mach() *am.Machine {
	return c.s.mach
}

// Render implements [h.Composer].
func (c *compo) Render() h.UI {
	w, ht := c.s.Size()

	return h.Div().
		ID(screenID).
		Class("tcell-screen").
		TabIndex(0).
		Aria("label", "terminal").
		DataSet("cols", strconv.Itoa(w)).
		DataSet("rows", strconv.Itoa(ht)).
		Body(h.Raw(c.s.gridHTML()))
}
