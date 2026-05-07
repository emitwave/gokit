// Package resource provides typed API resource transformers — a
// translation layer between your domain models and the JSON shape
// you expose to clients.
//
// The pattern is two pieces:
//
//  1. A Transformer[T] function that maps a model T to its public DTO
//  2. Item / Collection / Paginated helpers that apply the transformer
//     and write the response in one call
//
// Example:
//
//	// Define once, near your model:
//	type UserDTO struct {
//	    ID    string    `json:"id"`
//	    Name  string    `json:"name"`
//	    Email string    `json:"email"`
//	    Joined time.Time `json:"joined_at"`
//	}
//
//	func UserTransformer(u *models.User) UserDTO {
//	    return UserDTO{
//	        ID: u.ID, Name: u.Name, Email: u.Email, Joined: u.CreatedAt,
//	    }
//	}
//
//	// Use anywhere:
//	resource.Item(w, http.StatusOK, user, UserTransformer)
//	resource.Collection(w, http.StatusOK, users, UserTransformer)
//	resource.Paginated(w, users, page, perPage, total, UserTransformer)
//
// Why a transformer function instead of an interface? Two reasons:
//   - Generics give us type safety on the input — you can't accidentally
//     pass a Post to UserTransformer
//   - Transformers can take additional context (current user, embed
//     options) by closing over them, without each model needing to
//     implement a fixed interface
package resource

import (
	"net/http"

	"github.com/emitwave/gokit/response"
)

// Transformer maps a single model T to its DTO. Return any since the DTO
// type varies per transformer; the JSON encoder handles the rest.
//
// Conventionally, define Transformer[T] functions next to the type they
// produce, and re-use them across handlers — that's where the consistency
// pays off.
type Transformer[T any] func(T) any

// Item renders a single model through the transformer and writes a
// success-enveloped response.
func Item[T any](w http.ResponseWriter, status int, model T, t Transformer[T]) {
	if status == http.StatusCreated {
		response.Created(w, t(model))
		return
	}
	if status == http.StatusOK {
		response.OK(w, t(model))
		return
	}
	// Other statuses fall through to a raw envelope so callers can use
	// e.g. 202 Accepted with a transformed payload.
	response.JSON(w, status, response.SuccessEnvelope{Data: t(model)})
}

// Collection renders a slice of models through the transformer.
// The transformed slice is serialized as a JSON array under "data".
func Collection[T any](w http.ResponseWriter, status int, models []T, t Transformer[T]) {
	out := transformAll(models, t)
	if status == http.StatusOK {
		response.OK(w, out)
		return
	}
	response.JSON(w, status, response.SuccessEnvelope{Data: out})
}

// Paginated renders a slice of models through the transformer and
// includes offset pagination meta. Most list endpoints want this.
func Paginated[T any](w http.ResponseWriter, models []T, page, perPage int, total int64, t Transformer[T]) {
	response.Paginated(w, transformAll(models, t), page, perPage, total)
}

// CursorPaginated renders a slice with cursor meta. Use for feeds and
// activity logs where offset paging gets expensive.
func CursorPaginated[T any](w http.ResponseWriter, models []T, nextCursor, prevCursor string, t Transformer[T]) {
	response.CursorPaginated(w, transformAll(models, t), nextCursor, prevCursor)
}

// transformAll is the inner loop both Collection and Paginated share.
// We pre-allocate so large response bodies don't trigger slice growth.
func transformAll[T any](models []T, t Transformer[T]) []any {
	out := make([]any, len(models))
	for i, m := range models {
		out[i] = t(m)
	}
	return out
}

// ---------- composition helpers -----------------------------------------

// With lets you build a transformer that adds extra fields on top of a
// base transformer. The base runs first, then your override merges into
// the result. The DTO must be a map[string]any for With to work — if you
// use struct DTOs, just write a new transformer instead.
//
//	withAdmin := resource.With(UserTransformer, func(u *models.User) map[string]any {
//	    return map[string]any{"is_admin": u.HasRole("admin")}
//	})
//
// Useful for endpoints that expose extra fields only to specific callers
// (admin views, owner views) without forking the base transformer.
func With[T any](base Transformer[T], extra func(T) map[string]any) Transformer[T] {
	return func(m T) any {
		baseOut, ok := base(m).(map[string]any)
		if !ok {
			// Base returned a struct/non-map — composition isn't possible
			// without reflection we'd rather not pay for. Return base
			// untouched so the caller at least gets correct data.
			return base(m)
		}
		for k, v := range extra(m) {
			baseOut[k] = v
		}
		return baseOut
	}
}
