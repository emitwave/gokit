package response

import (
	"net/http"
	"strconv"
)

// PageMeta is the meta block for offset-paginated responses. The field
// names are the conventional `page` / `per_page` / `total` / `pages`
// shape so frontends don't need a translation layer.
type PageMeta struct {
	Page    int   `json:"page"`
	PerPage int   `json:"per_page"`
	Total   int64 `json:"total"`
	Pages   int   `json:"pages"` // total page count
}

// CursorMeta is the meta block for cursor-paginated responses. NextCursor
// is empty when there's no next page; clients use that as the stop signal.
type CursorMeta struct {
	NextCursor string `json:"next_cursor,omitempty"`
	PrevCursor string `json:"prev_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

// Paginated writes 200 with data + offset pagination meta.
//
//	users := repo.List(page, perPage)
//	total := repo.Count()
//	response.Paginated(w, users, page, perPage, total)
func Paginated(w http.ResponseWriter, data any, page, perPage int, total int64) {
	pages := 0
	if perPage > 0 {
		pages = int((total + int64(perPage) - 1) / int64(perPage))
	}
	OKWithMeta(w, data, map[string]any{
		"pagination": PageMeta{
			Page:    page,
			PerPage: perPage,
			Total:   total,
			Pages:   pages,
		},
	})
}

// CursorPaginated writes 200 with data + cursor meta. Pass empty strings
// for cursors that don't exist (e.g. on the first page, prev is empty).
func CursorPaginated(w http.ResponseWriter, data any, nextCursor, prevCursor string) {
	OKWithMeta(w, data, map[string]any{
		"pagination": CursorMeta{
			NextCursor: nextCursor,
			PrevCursor: prevCursor,
			HasMore:    nextCursor != "",
		},
	})
}

// ---------- query parsing -----------------------------------------------

// PageQuery extracts page + per_page from r.URL.Query() with safe defaults
// and bounds. Use it at the top of list handlers:
//
//	page, perPage := response.PageQuery(r, 25, 100) // default 25, max 100
//
// Always-positive page (>= 1) and clamps per_page between 1 and max so
// clients can't ask for unbounded slices.
func PageQuery(r *http.Request, defaultPerPage, maxPerPage int) (page, perPage int) {
	q := r.URL.Query()

	page, _ = strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}

	perPage, _ = strconv.Atoi(q.Get("per_page"))
	if perPage < 1 {
		perPage = defaultPerPage
	}
	if perPage > maxPerPage {
		perPage = maxPerPage
	}
	return page, perPage
}
