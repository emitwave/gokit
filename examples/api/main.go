// Combined example: a small users API built with every gokit package.
//
// Run it:    go run ./examples/api
// Try it:    curl -i http://localhost:8080/api/v1/users
//            curl -i http://localhost:8080/api/v1/users/1
//            curl -i http://localhost:8080/api/v1/users/999     # 404 from binder
//            curl -i -X POST -H 'Content-Type: application/json' \
//                    -d '{"name":"","email":"nope","age":10}' \
//                    http://localhost:8080/api/v1/users
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"sync"

	"github.com/emitwave/gokit/logger"
	"github.com/emitwave/gokit/resource"
	"github.com/emitwave/gokit/response"
	"github.com/emitwave/gokit/router"
	"github.com/emitwave/gokit/security"
	"github.com/emitwave/gokit/validation"
)

// ---------- domain types ------------------------------------------------

type User struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Age   int    `json:"age"`
	pwHash string // never serialized
}

type UserDTO struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Age   int    `json:"age"`
}

func UserTransformer(u *User) any {
	return UserDTO{ID: u.ID, Name: u.Name, Email: u.Email, Age: u.Age}
}

// ---------- in-memory "store" -------------------------------------------

type userStore struct {
	mu     sync.Mutex
	users  []*User
	nextID int
}

func newUserStore() *userStore {
	return &userStore{nextID: 3, users: []*User{
		{ID: 1, Name: "Tunde A.", Email: "tunde@emitwave.com", Age: 28},
		{ID: 2, Name: "Aisha B.", Email: "aisha@emitwave.com", Age: 31},
	}}
}

func (s *userStore) all() []*User {
	s.mu.Lock(); defer s.mu.Unlock()
	out := make([]*User, len(s.users))
	copy(out, s.users)
	return out
}

func (s *userStore) get(id int) (*User, bool) {
	s.mu.Lock(); defer s.mu.Unlock()
	for _, u := range s.users {
		if u.ID == id {
			return u, true
		}
	}
	return nil, false
}

func (s *userStore) create(name, email string, age int) *User {
	s.mu.Lock(); defer s.mu.Unlock()
	u := &User{ID: s.nextID, Name: name, Email: email, Age: age}
	s.nextID++
	s.users = append(s.users, u)
	return u
}

func (s *userStore) delete(id int) bool {
	s.mu.Lock(); defer s.mu.Unlock()
	for i, u := range s.users {
		if u.ID == id {
			s.users = append(s.users[:i], s.users[i+1:]...)
			return true
		}
	}
	return false
}

// ---------- handlers ----------------------------------------------------
//
// Note how Show/Destroy now retrieve the bound user instead of fetching
// it themselves — model binding ran the lookup before the handler started,
// and 404s for missing IDs are handled centrally by the binding layer.

func makeHandlers(store *userStore) router.ResourceController {
	return router.ResourceController{
		Index: func(w http.ResponseWriter, r *http.Request) {
			page, perPage := response.PageQuery(r, 25, 100)
			all := store.all()

			start := (page - 1) * perPage
			end := start + perPage
			if start > len(all) { start = len(all) }
			if end > len(all)   { end   = len(all) }

			resource.Paginated(w, all[start:end], page, perPage, int64(len(all)), UserTransformer)
		},

		Show: func(w http.ResponseWriter, r *http.Request) {
			// The binder already loaded the user (or returned 404). All
			// we have to do here is render the typed result.
			u, ok := router.Bound[*User](r, "user")
			if !ok {
				response.InternalError(w, r, "binding missing")
				return
			}
			resource.Item(w, http.StatusOK, u, UserTransformer)
		},

		Store: func(w http.ResponseWriter, r *http.Request) {
			var input map[string]any
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				response.BadRequest(w, r, "request body must be valid JSON")
				return
			}
			errs := validation.Make(input, validation.Rules{
				"name":  "required|string|min:2|max:100",
				"email": "required|email",
				"age":   "required|integer|between:13,120",
			})
			if errs.Any() {
				response.ValidationFailed(w, r, errs)
				return
			}
			u := store.create(
				input["name"].(string),
				input["email"].(string),
				int(input["age"].(float64)),
			)
			logger.FromContext(r.Context()).Info("user created", "user_id", u.ID)
			resource.Item(w, http.StatusCreated, u, UserTransformer)
		},

		Destroy: func(w http.ResponseWriter, r *http.Request) {
			u, _ := router.Bound[*User](r, "user")
			store.delete(u.ID)
			logger.FromContext(r.Context()).Info("user deleted", "user_id", u.ID)
			response.NoContent(w)
		},

		// Use the {user} segment so it lines up with the binder name
		// registered below — that's what wires "load me a User" to the
		// path parameter.
		ParamName: "user",
	}
}

// ---------- main --------------------------------------------------------

func main() {
	log := logger.NewFromEnv()
	logger.SetDefault(log)

	store := newUserStore()
	r := router.New()

	// Global middleware. Order matters — RequestID first so everything
	// downstream (incl. Recovery's logger fields and the request log
	// summary) carries the correlation key.
	r.Use(
		security.RequestID(),
		security.Recovery(security.RecoveryOptions{Logger: slogPanicAdapter{log}}),
		logger.HTTPMiddleware(log, logger.HTTPOptions{SkipPaths: []string{"/healthz"}}),
		security.Headers(),
		security.CORS(security.CORSOptions{
			AllowedOrigins: []string{"*"},
			MaxAge:         600,
		}),
		security.RateLimit(security.RateLimitOptions{RPS: 20, Burst: 40}),
	)

	// Model binding: any {user} segment in any route gets pre-resolved.
	// The handler retrieves the loaded *User via router.Bound[*User].
	r.Bind("user", func(_ context.Context, idStr string) (any, error) {
		id, err := strconv.Atoi(idStr)
		if err != nil {
			return nil, router.ErrBindingNotFound
		}
		u, found := store.get(id)
		if !found {
			return nil, router.ErrBindingNotFound
		}
		return u, nil
	})

	// Wire binding errors to the response package so they get the same
	// envelope as everything else.
	r.BindingNotFoundHandler(func(w http.ResponseWriter, req *http.Request) {
		response.NotFound(w, req, "")
	})

	// Fallback for unmatched paths — same JSON envelope as binding 404s.
	r.Fallback(func(w http.ResponseWriter, req *http.Request) {
		response.NotFound(w, req, "")
	})

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		response.OK(w, map[string]string{"status": "ok"})
	}).Name("health")

	// Resource routes — one call registers Index/Show/Store/Destroy.
	api := r.Group("/api/v1")
	api.Resource("/users", makeHandlers(store))

	log.Info("starting server", "addr", ":8080")
	log.Info("registered routes\n" + r.PrintRoutes())

	if err := http.ListenAndServe(":8080", r); err != nil {
		log.Error("server failed", "error", err.Error())
	}
}

// slogPanicAdapter bridges *slog.Logger to security.PanicLogger.
type slogPanicAdapter struct{ l *slog.Logger }

func (a slogPanicAdapter) Error(msg string, fields ...any) { a.l.Error(msg, fields...) }
