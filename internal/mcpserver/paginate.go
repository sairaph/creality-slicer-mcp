package mcpserver

import (
	"github.com/sairaph/mcp-wizard/budget"
	"github.com/sairaph/mcp-wizard/render"
)

// paginateBudget bounds the rendered token size of one page: generous enough
// that a page holds many records, small enough that several pages plus their
// shared frontmatter stay well under render.MaxBytes.
const paginateBudget = 4000

// paginatePage runs budget.Paginate over records at page (1-indexed, matching
// render.PageMeta), rendering each candidate window with renderRows (the
// table or list body for just that window, no frontmatter). It returns the
// window for page, the PageMeta for the frontmatter, and the " Next: page=N."
// suffix (render.NextPageHint) to append to the body. A page out of range
// yields a nil window and a PageMeta describing the valid range, not an error.
func paginatePage[T any](records []T, page int, renderRows func([]T) (string, error)) (window []T, meta render.PageMeta, nextHint string, err error) {
	if page < 1 {
		page = 1
	}
	window, totalPages, err := budget.Paginate(records, page, paginateBudget, renderRows)
	if err != nil {
		return nil, render.PageMeta{}, "", err
	}
	meta = render.PageMeta{Page: page, Total: len(records), TotalPages: totalPages}
	return window, meta, render.NextPageHint(meta), nil
}
