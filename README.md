# gokit

Production helper packages for Go services. A private toolkit you can
pull into any project — six packages that compose into a complete API
foundation.

```
github.com/emitwave/gokit
├── validation/      # Declarative rule strings + struct tag validation
├── security/        # Composable middleware (headers, CORS, rate limit, recovery, request ID)
├── response/        # JSON API envelopes (success, error, pagination)
├── resource/        # Typed model→DTO transformers using generics
├── logger/          # Structured logging on slog + HTTP request log middleware
├── router/          # net/http-based routing with groups, named routes, URL generation
├── session/         # Cookie-based sessions with pluggable Store, flash messages
├── events/          # Sync/async event dispatcher with type-safe listeners
├── queue/           # Background job processing with retries and pluggable Backend
├── notifications/   # Multi-channel notification dispatch (mail/SMS/database/...)
├── filesystem/      # Storage abstraction (local + memory; cloud-backend ready)
├── cache/           # Key/value cache + Lock/Locker (memory; Redis-backend ready)
├── helpers/         # Scalar / control-flow helpers (Value, Blank, Tap, Retry, ...)
│   ├── arr/         # Array & map helpers (Get/Set with dot paths, Map/Filter/KeyBy/...)
│   ├── num/         # Number formatting (Format, Currency, FileSize, Ordinal, ...)
│   └── data/        # Path-based access on nested structures with wildcards
└── examples/
    ├── api/         # Combined example using the HTTP-side packages
    ├── security/
    └── validation/
```

All packages require Go 1.26+ (built and tested against 1.26.2).

## Installation (private module)

```bash
# In your shell profile:
export GOPRIVATE=github.com/emitwave/*
git config --global url."git@github.com:emitwave/".insteadOf "https://github.com/emitwave/"

# In any consuming project:
go get github.com/emitwave/gokit@latest
```

For CI, give the runner a PAT or deploy key with read access and set
`GOPRIVATE` in the workflow environment:

```yaml
- name: Configure private modules
  run: |
    git config --global url."https://${{ secrets.GH_PAT }}:x-oauth-basic@github.com/".insteadOf "https://github.com/"
    echo "GOPRIVATE=github.com/emitwave/*" >> $GITHUB_ENV
```

## The combined picture

```go
import (
    "github.com/emitwave/gokit/logger"
    "github.com/emitwave/gokit/resource"
    "github.com/emitwave/gokit/response"
    "github.com/emitwave/gokit/router"
    "github.com/emitwave/gokit/security"
    "github.com/emitwave/gokit/validation"
)

func main() {
    log := logger.NewFromEnv()
    r := router.New()

    r.Use(
        security.RequestID(),
        security.Recovery(security.RecoveryOptions{Logger: panicAdapter{log}}),
        logger.HTTPMiddleware(log),
        security.Headers(),
        security.CORS(security.CORSOptions{AllowedOrigins: []string{"https://app.emitwave.com"}}),
        security.RateLimit(security.RateLimitOptions{RPS: 20, Burst: 40}),
    )

    api := r.Group("/api/v1")
    api.Get("/users/{id}", showUser).Name("users.show")
    api.Post("/users", createUser).Name("users.store")

    http.ListenAndServe(":8080", r)
}
```

A complete working version is in `examples/api/main.go` — `go run ./examples/api`.

---

## `validation`

Two ways in, both produce the same `*Errors` bag:

```go
// Map-based (pipe-separated rule strings)
errs := validation.Make(data, validation.Rules{
    "email":    "required|email",
    "password": "required|string|min:8|confirmed",
    "role":     "required|in:admin,user,guest",
})

// Struct-based (validate: tags)
type Req struct {
    Email string `json:"email" validate:"required,email"`
}
errs := validation.ValidateStruct(req)
```

Built-in rules: `required`, `nullable`, `string`, `numeric`, `integer`,
`boolean`, `email`, `url`, `uuid`, `alpha`, `alpha_num`, `alpha_dash`,
`min`, `max`, `between`, `in`, `not_in`, `regex`, `confirmed`, `same`,
`different`, `date`, `before`, `after`, `json`, `ip`, `starts_with`,
`ends_with`. Register custom rules with `validation.Register`.

`*Errors` implements `error` and `json.Marshaler`, marshaling to
`{"field":["msg",…]}`.

---

## `security`

Composable `net/http` middleware. Works with stdlib mux, chi, gorilla, gin
(via adapter), or this kit's own `router`.

| Middleware | Purpose |
|------------|---------|
| `RequestID` | Generates / propagates `X-Request-ID`, stores it on context |
| `Recovery` | Catches panics → 500, logs with stack |
| `Headers` | Helmet-style hardened defaults (CSP, HSTS, frame, MIME, COOP, CORP) |
| `CORS` | Origin allowlist, preflight, credentials |
| `RateLimit` | In-memory token bucket per IP (or any key); swap for Redis in multi-instance setups |
| `Chain` | Compose middleware into a single layer |

```go
stack := security.Chain(
    security.RequestID(),
    security.Recovery(security.RecoveryOptions{Logger: log}),
    security.Headers(),
    security.CORS(security.CORSOptions{AllowedOrigins: []string{"https://app.emitwave.com"}}),
    security.RateLimit(security.RateLimitOptions{RPS: 10, Burst: 20}),
)
```

---

## `response`

Consistent JSON envelopes for success and error.

```go
// Success
response.OK(w, user)                       // 200 { "data": ... }
response.Created(w, user)                  // 201
response.NoContent(w)                      // 204
response.OKWithMeta(w, items, meta)        // 200 { "data": [...], "meta": {...} }
response.Paginated(w, items, page, perPage, total)
response.CursorPaginated(w, items, nextCursor, prevCursor)

// Error — request_id auto-included from context
response.NotFound(w, r, "")                // 404 with sensible default message
response.BadRequest(w, r, "id required")
response.Forbidden(w, r, "")
response.Conflict(w, r, "email exists")
response.TooManyRequests(w, r, "")
response.InternalError(w, r, "")
response.ValidationFailed(w, r, errs)      // 422 with the validation bag

// Custom code
response.Error(w, r, 418, "im_a_teapot", "I refuse to brew coffee.")
```

Error envelope:

```json
{
  "error": {
    "code": "validation_failed",
    "message": "The given data was invalid.",
    "details": {"email": ["The email field is required."]}
  },
  "request_id": "f3b7b172-b7d7-4390-a522-fd7e3f16d866"
}
```

`response.PageQuery(r, defaultPerPage, maxPerPage)` parses `?page=` and
`?per_page=` with safe bounds (no negative pages, no unbounded slices).

---

## `resource`

Typed transformers that map domain models to public DTOs. Generic-based,
no interface required on your models.

```go
type UserDTO struct {
    ID    string `json:"id"`
    Email string `json:"email"`
}

func UserTransformer(u *models.User) any {
    return UserDTO{ID: u.ID, Email: u.Email}
    // private fields like u.PasswordHash never make it into JSON
}

resource.Item(w, http.StatusOK, user, UserTransformer)
resource.Collection(w, http.StatusOK, users, UserTransformer)
resource.Paginated(w, users, page, perPage, total, UserTransformer)
resource.CursorPaginated(w, users, next, prev, UserTransformer)

// Compose extra fields (e.g. admin views)
withAdmin := resource.With(UserTransformer, func(u *models.User) map[string]any {
    return map[string]any{"is_admin": u.HasRole("admin")}
})
resource.Item(w, http.StatusOK, user, withAdmin)
```

---

## `logger`

Structured logging on `log/slog`. Adds three things over raw slog:

```go
// 1. Sensible default handlers
log := logger.New(logger.Config{
    Format:     "json",        // "json" or "pretty"
    Level:      slog.LevelInfo,
    RedactKeys: []string{"password", "authorization"},
})

// or env-driven:
log := logger.NewFromEnv()  // reads LOG_LEVEL, LOG_FORMAT

// 2. Context propagation
ctx = logger.IntoContext(ctx, log)
log = logger.FromContext(ctx)  // never nil, falls back to slog.Default()

// 3. HTTP request middleware
mux = logger.HTTPMiddleware(log)(mux)
mux = logger.HTTPMiddleware(log, logger.HTTPOptions{
    SkipPaths: []string{"/healthz"},
})(mux)
```

The HTTP middleware:
- Pre-loads each request's logger with `request_id`, `method`, `path`
- Logs every response with status, bytes, duration, remote IP, UA
- 200–399 → INFO, 400–499 → WARN, 500+ → ERROR
- Skips configured paths (`/healthz`, `/metrics`)

In handlers:

```go
func showUser(w http.ResponseWriter, r *http.Request) {
    log := logger.FromContext(r.Context())
    log.Info("looking up user", "user_id", id)
    // automatically also includes request_id, method, path
}
```

---

## `router`

Built on the stdlib `net/http.ServeMux` (method-aware patterns, added
in Go 1.22) plus groups, named routes, URL generation, per-route
middleware, host scoping, fallback, resource routes, and model binding.
Zero external deps.

### Basic routing

```go
r := router.New()

r.Use(security.RequestID(), security.Recovery(...))

r.Get("/", home).Name("home")

api := r.Group("/api/v1")
api.Use(authMiddleware)
api.Get("/users/{id}", show).Name("users.show")
api.Post("/users", create).Name("users.store")

// Path params with type conversion
id, ok := router.PathParamInt(r, "id")
```

### Named routes & URL generation

```go
r.URL("users.show", "id", "42")                    // → "/api/v1/users/42"
r.URL("users.show", "id", "42", "expand", "posts") // → "/api/v1/users/42?expand=posts"
```

### Per-route middleware

Apply middleware to a single endpoint without putting it on the whole group:

```go
api.Get("/admin/dashboard", dashboard).Middleware(requireSuperuser)
```

Per-route middleware runs INSIDE group and global middleware. Add it any
time before the route's first request — afterwards, the chain is frozen.

### Named middleware (aliases & groups)

For larger apps, registering middleware once under a short name and
referencing it across many routes is cleaner than passing the same
function around. Mirrors Laravel's `$routeMiddleware` / `$middlewareGroups`:

```go
// Register aliases once at startup
r.AliasMiddleware("auth", authMW)
r.AliasMiddleware("throttle", rateLimit(100, time.Minute))
r.AliasMiddleware("admin", requireAdmin)

// Bundle aliases into named groups
r.MiddlewareGroup("api", "auth", "throttle")
r.MiddlewareGroup("admin-area", "auth", "admin")  // groups can include aliases
r.MiddlewareGroup("api-v2", "api", "version-header") // ...and other groups

// Reference by name — on routes:
r.Get("/dashboard", dashboard).WithMiddleware("auth")
r.Get("/admin/users", users).WithMiddleware("admin-area")  // expands to auth + admin

// On groups:
api := r.Group("/api/v1")
api.UseAlias("api")  // wraps the whole group in auth + throttle

// On global middleware (escape hatch — Aliased returns []Middleware):
r.Use(r.Aliased("logging", "request-id")...)
```

Resolution happens at call time; unknown names panic loud — that catches
typos at startup rather than silently dropping middleware. Re-aliasing
the same name replaces the prior binding, which is handy in tests where
you want to swap a real middleware for a fake.

### Resource routes

One call registers the standard CRUD set with named routes per action:

```go
rr := api.Resource("/users", router.ResourceController{
    Index:   userIndex,    // GET    /users         users.index
    Show:    userShow,     // GET    /users/{id}    users.show
    Store:   userCreate,   // POST   /users         users.store
    Update:  userUpdate,   // PUT    /users/{id}    users.update
    Destroy: userDelete,   // DELETE /users/{id}    users.destroy
    // Optional HTML form actions:
    // Create: userCreateForm,  // GET /users/create       users.create
    // Edit:   userEditForm,    // GET /users/{id}/edit    users.edit

    ParamName:  "user",     // path param + binder key (default: "id")
    AllowPatch: true,       // also register PATCH /users/{id}
})

// Per-action overrides
rr.Update.Middleware(requireAdmin)
rr.Destroy.Middleware(requireSuperuser)
```

Nil handler fields are skipped — that's how you opt out of an action.

### Model binding

Register a binder once, get the resolved model on context for every route
that uses the matching path parameter:

```go
r.Bind("user", func(ctx context.Context, id string) (any, error) {
    u, err := userRepo.FindByID(ctx, id)
    if errors.Is(err, sql.ErrNoRows) {
        return nil, router.ErrBindingNotFound  // → 404
    }
    return u, err
})

// Wire 404s into your response envelope
r.BindingNotFoundHandler(func(w http.ResponseWriter, req *http.Request) {
    response.NotFound(w, req, "")
})

r.Get("/users/{user}", func(w http.ResponseWriter, req *http.Request) {
    u, _ := router.Bound[*models.User](req, "user")  // already loaded
    response.OK(w, u)
})
```

`Bound[T]` is a generic — you can ask for any type the binder might
return. Mismatched types cleanly return `(zero, false)` rather than panic.

### Host / subdomain routing

```go
api := r.Host("api.emitwave.com")
api.Get("/users", index)        // matches only when Host: api.emitwave.com

admin := r.Host("admin.emitwave.com").Group("/v1")
admin.Use(requireAdmin)
admin.Get("/dashboard", dash)
```

⚠️ The stdlib ServeMux supports **exact** host match only — wildcard
hosts (`{tenant}.example.com`) are treated as literal strings. For
multi-tenant subdomains, register each subdomain explicitly or do the
host parsing in middleware.

### Fallback for unmatched routes

```go
r.Fallback(func(w http.ResponseWriter, req *http.Request) {
    response.NotFound(w, req, "")
})
```

Without this, the stdlib mux returns a plain "404 page not found" string —
fine for development, but you'll want the JSON envelope in production.
Method-mismatch (405) is unaffected; only true no-match goes to fallback.

### Inspecting the route table

`r.PrintRoutes()` returns a tabular listing — handy for a CLI subcommand
or a debug endpoint:

```
GET     *                    /healthz                                 health
GET     *                    /api/v1/users                            users.index
POST    *                    /api/v1/users                            users.store
GET     *                    /api/v1/users/{user}                     users.show
DELETE  *                    /api/v1/users/{user}                     users.destroy
```

(`*` means "any host"; explicit host scoping shows the host instead.)

---

## `session`

Cookie-based HTTP sessions with a pluggable server-side store, flash
messages, and ID rotation for post-login fixation defense.

```go
import "github.com/emitwave/gokit/session"

mgr := session.NewManager(session.Config{
    Store:      session.NewMemoryStore(),  // swap for Redis in prod
    CookieName: "emitwave_session",
    Lifetime:   24 * time.Hour,
    Secure:     true,
    HTTPOnly:   true,
    SameSite:   http.SameSiteLaxMode,
})

r := router.New()
r.Use(mgr.Middleware)

r.Get("/login", func(w http.ResponseWriter, r *http.Request) {
    s := session.From(r.Context())
    s.Regenerate(r.Context())             // new ID after auth
    s.Set("user_id", u.ID)
    s.Flash("status", "Welcome back!")    // shown once, on next request
    http.Redirect(w, r, "/", 302)
})

r.Get("/", func(w http.ResponseWriter, r *http.Request) {
    s := session.From(r.Context())
    if msg := s.GetFlash("status"); msg != nil {
        // ... render banner
    }
})

r.Post("/logout", func(w http.ResponseWriter, r *http.Request) {
    session.From(r.Context()).Destroy()    // delete from store + clear cookie
})
```

Stores ship with: `MemoryStore` (fine for dev / single-instance). The
`Store` interface is three methods (`Find`, `Save`, `Delete`) so a Redis
or SQL backend is straightforward.

The middleware is smart about read-only requests — it doesn't mint a
session for healthcheck pings or other paths that don't actually write
anything to the session.

---

## `events`

In-process event dispatcher with sync + async dispatch and type-safe
subscription via generics.

```go
import "github.com/emitwave/gokit/events"

type UserRegistered struct {
    UserID string
    Email  string
}
func (UserRegistered) Name() string { return "user.registered" }

d := events.New()

// Type-safe listener — gets a typed event, no assertions
events.Subscribe(d, func(ctx context.Context, e UserRegistered) error {
    return mailer.SendWelcome(ctx, e.Email)
})
events.Subscribe(d, func(ctx context.Context, e UserRegistered) error {
    return crm.Track(ctx, e.UserID, "registered")
})

// Sync — errors aggregated, every listener still runs
if err := d.Dispatch(ctx, UserRegistered{UserID: u.ID, Email: u.Email}); err != nil {
    log.Error("listener failures", "err", err)
}

// Async — listeners run in goroutines; pass a logger fn to surface failures
d.DispatchAsync(ctx, UserRegistered{...}, func(err error) {
    log.Error("async listener failed", "err", err)
})
d.Wait()  // call during shutdown to drain in-flight async listeners
```

A failing listener doesn't short-circuit the rest — every listener for an
event always gets to run, and errors come back as `errors.Join`. Panics in
async listeners are recovered and surfaced via the error callback.

---

## `queue`

Background job processing with typed handlers, retries with exponential
backoff, delayed dispatch, and a `Backend` interface for swapping
storage.

```go
import "github.com/emitwave/gokit/queue"

q := queue.New(queue.NewMemoryBackend(), queue.Config{
    Workers:     8,
    MaxRetry:    3,
    BaseBackoff: time.Second,    // 1s, 2s, 4s, ...
    MaxBackoff:  5 * time.Minute,
    ErrorHandler: func(env *queue.Envelope, err error, willRetry bool) {
        log.Error("job failed",
            "type", env.Type, "attempt", env.Attempt,
            "will_retry", willRetry, "err", err,
        )
    },
})

type WelcomePayload struct {
    To, UserName string
}

q.Register("send-welcome-email", func(ctx context.Context, payload []byte) error {
    var p WelcomePayload
    if err := json.Unmarshal(payload, &p); err != nil { return err }
    return mailer.Send(ctx, p.To, "Welcome!", p.UserName)
})

go q.Run(ctx)  // start workers; Stop() to drain on shutdown

// From a handler, anywhere:
queue.Dispatch(ctx, q, "send-welcome-email", WelcomePayload{
    To: u.Email, UserName: u.Name,
})

// Schedule for later:
queue.DispatchAfter(ctx, q, "follow-up", payload, 24*time.Hour)
queue.DispatchAt(ctx, q, "send-reminder", payload, dueDate)
```

The `MemoryBackend` is process-local — fine for dev and single-instance
production. For multi-instance, implement `Backend` against Redis (BLMOVE
for the worker loop) or SQS (visibility timeouts replace explicit ack/nack).
The `Queue` itself owns the retry policy, so backends only need to handle
storage.

Handler panics are recovered and counted as failed attempts (same retry
treatment as returned errors). Unregistered job types go straight to the
dead-letter queue rather than retry-looping.

---

## `notifications`

Multi-channel notification dispatch — one notification can fan out to
mail, SMS, in-app database storage, Slack, push, anything you implement.

```go
import "github.com/emitwave/gokit/notifications"

type WelcomeEmail struct{ UserName string }

func (WelcomeEmail) Channels() []string { return []string{"mail", "database"} }
func (n WelcomeEmail) Render(channel string) (notifications.Message, error) {
    switch channel {
    case "mail":
        return notifications.MailMessage{
            Subject: "Welcome!",
            Body:    "Hi " + n.UserName + ", thanks for joining.",
        }, nil
    case "database":
        return notifications.DatabaseMessage{
            Type: "welcome",
            Data: map[string]any{"name": n.UserName},
        }, nil
    }
    return nil, nil
}

// Recipients implement Notifiable — one method, returns the channel-
// specific address (email, phone, user ID, ...).
type User struct{ ID, Email, Phone string }
func (u User) RouteFor(channel string) string {
    switch channel {
    case "mail":     return u.Email
    case "sms":      return u.Phone
    case "database": return u.ID
    }
    return ""
}

n := notifications.New()
n.Register(notifications.NewLogChannel(log))   // dev — logs everything
n.Register(yourMailChannel)                     // your SMTP/SES/Mailgun adapter
n.Register(yourDatabaseChannel)                 // writes to in-app inbox

n.Send(ctx, WelcomeEmail{UserName: u.Name}, u)
n.Send(ctx, BulkAnnouncement{...}, allUsers...)  // fan out to many recipients
```

Built-in `Message` types: `MailMessage` (subject + body), `SMSMessage`,
`DatabaseMessage`. Custom channels can define their own message types
and assert on `Kind()` to validate input.

For async delivery, wrap `Send` in `queue.Dispatch` — the queue handles
retries; the notifier handles fan-out and routing.

The `LogChannel` is registered as `"log"` by default, but
`NewLogChannelAs("mail", logger)` lets you shadow real channel names so
in dev you can see what *would* have gone out without configuring
real providers.

---

## `filesystem`

A backend-agnostic file storage abstraction. The shape mirrors Laravel's
Storage facade (multiple named disks, common operations on each), but
the interface is small enough that plugging in cloud backends — S3, R2,
MinIO, GCS — is a few hundred lines, not a porting project.

```go
import "github.com/emitwave/gokit/filesystem"

// Construct disks
local := filesystem.NewLocalDisk("/var/app/storage")
uploads := filesystem.NewLocalDisk("/var/app/uploads")

// Register with a manager (one named "local" is the default)
mgr := filesystem.NewManager("local", local)
mgr.Register("uploads", uploads)

// Use a specific disk
err := mgr.Disk("uploads").Put(ctx, "avatars/u1.jpg", data)

// Or the default
contents, err := mgr.Default().Get(ctx, "config/app.json")
```

### Operations

The `Disk` interface covers the common file operations:

```go
// Read
Get(ctx, path) ([]byte, error)
Reader(ctx, path) (io.ReadCloser, error)
Exists(ctx, path) (bool, error)
Stat(ctx, path) (FileInfo, error)

// Write — Put and Writer are atomic (write-to-temp + rename)
Put(ctx, path, data) error
Writer(ctx, path) (io.WriteCloser, error)
Append(ctx, path, data) error
Prepend(ctx, path, data) error

// Mutation
Delete(ctx, path) error
Copy(ctx, src, dst) error
Move(ctx, src, dst) error

// Directories
MakeDirectory(ctx, path) error
DeleteDirectory(ctx, path) error    // recursive
Files(ctx, dir) ([]string, error)        // immediate children
AllFiles(ctx, dir) ([]string, error)     // recursive
Directories(ctx, dir) ([]string, error)
```

### Streaming

For large files, use `Reader` and `Writer` instead of `Get`/`Put`:

```go
r, err := disk.Reader(ctx, "videos/large.mp4")
if err != nil { return err }
defer r.Close()
io.Copy(w, r)

// Atomic streaming write — file isn't visible until Close succeeds
w, err := disk.Writer(ctx, "uploads/big.csv")
if err != nil { return err }
defer w.Close()  // must be checked! atomic commit happens here
io.Copy(w, src)
```

### Path safety

`LocalDisk` rejects path traversal attempts at the API layer — any
`..` segment, absolute path, or path that would resolve outside the
configured root returns `ErrInvalidPath`. This means user-supplied
upload paths can flow into `disk.Put` directly without an additional
allowlist check, as long as you've already authenticated and
authorized the user.

```go
disk := filesystem.NewLocalDisk("/var/app/storage")
err := disk.Put(ctx, "../../etc/passwd", evilBytes)  // ErrInvalidPath
```

### Capability interfaces

Some operations only make sense on certain backends. Rather than bloat
the `Disk` interface with methods that always return "unsupported", we
expose them as optional capability interfaces — detect via type assertion:

```go
// PathProvider — backend can return a real filesystem path (LocalDisk does;
// cloud disks don't)
if pp, ok := disk.(filesystem.PathProvider); ok {
    p, _ := pp.Path("uploads/avatar.jpg")
    // shell out to a local image converter, etc.
}

// URLProvider — backend can produce a public URL (CDN-fronted disks)
if up, ok := disk.(filesystem.URLProvider); ok {
    publicURL, _ := up.URL("public/banner.png")
}

// SignedURLProvider — backend can produce time-limited signed URLs
// (S3 presigned, GCS signed, etc.)
if sp, ok := disk.(filesystem.SignedURLProvider); ok {
    url, _ := sp.SignedURL(ctx, "private/doc.pdf", time.Now().Add(15*time.Minute))
}
```

### Testing

`MemoryDisk` is a fully in-memory implementation with the same
contract as `LocalDisk`. Same compliance suite runs against both, so
swapping `MemoryDisk` in for tests gives you the real behavior without
touching disk:

```go
func TestUploadHandler(t *testing.T) {
    disk := filesystem.NewMemoryDisk()
    handler := NewUploadHandler(disk)
    // ... exercise handler ...
    if ok, _ := disk.Exists(ctx, "uploads/test.jpg"); !ok {
        t.Error("upload should have been stored")
    }
}
```

### What's intentionally NOT in `filesystem`

- **Cloud backends (S3, GCS, R2, MinIO)**: implementing `Disk` against
  any of these clients is straightforward. We don't include them
  in-tree because every team has different preferences (raw aws-sdk-go-v2
  vs `minio-go` vs `cloud.google.com/go/storage`) and we'd rather not
  pin a dependency choice.
- **Visibility flags ("public" / "private")**: this is an S3-shaped
  concept that doesn't translate cleanly to local filesystems. Set
  bucket policies / file modes outside the disk abstraction.
- **HTTP serving**: `http.FileServer` exists in stdlib. If you need
  `Storage::download(path)`, build that adapter against the `response`
  package and a `disk.Reader` call.

---

## `cache`

A backend-agnostic key/value cache with TTL, atomic counters, and a
distributed-lock primitive. Same shape as `filesystem` — interface +
in-memory implementation + manager for multiple named stores. Plug in
Redis / Valkey / Memcached by implementing the `Store` interface against
your client of choice.

```go
import "github.com/emitwave/gokit/cache"

mem := cache.NewMemoryStore()
mgr := cache.NewManager("memory", mem)

// Use a specific store
err := mgr.Default().Put(ctx, "view-count", []byte("42"), time.Hour)

// The byte-oriented Store interface is the lingua franca; for typed
// values use the generic helpers — JSON-encoded by default.
err := cache.Put(ctx, mgr.Default(), "user:42", user, time.Hour)
u, err := cache.Get[*User](ctx, mgr.Default(), "user:42")
if errors.Is(err, cache.ErrNotFound) { /* miss */ }
```

### `Remember` — the canonical "get or compute" pattern

The single most useful cache operation in real apps. If the value is
present, return it; otherwise compute, cache, and return:

```go
user, err := cache.Remember(ctx, store, "user:42", time.Hour,
    func(ctx context.Context) (*User, error) {
        return userRepo.FindByID(ctx, 42)
    })

// No TTL — cache "until something explicitly Forgets it":
config, err := cache.RememberForever(ctx, store, "app-config",
    func(ctx context.Context) (*Config, error) {
        return loadConfigFromDisk()
    })
```

Errors from the compute function are returned as-is and are NOT
cached — failures shouldn't poison the cache. If you need at-most-once
execution across instances (so concurrent cache misses don't all hit
the database simultaneously), wrap the compute step in a `Locker`:

```go
err := cache.WithLock(ctx, locker, "rebuild:user:42", 30*time.Second,
    func(ctx context.Context) error {
        // Inside the lock — re-check the cache, then compute if still missing.
        _, err := cache.Remember(ctx, store, "user:42", time.Hour, fetchUser)
        return err
    })
```

### Counters — atomic `Increment` / `Decrement`

```go
n, _ := store.Increment(ctx, "page-views", 1)   // creates if missing, starts at delta
n, _ = store.Decrement(ctx, "credits", 5)       // delta is positive: subtract 5
```

The MemoryStore uses its mutex to make this atomic. A Redis-backed
Store would use `INCRBY` / `DECRBY`. Counters auto-create at delta if
the key doesn't exist; pass an integer-string pre-Put if you need an
explicit initial value with TTL.

### Other operations

```go
// Conditional write — first caller wins
ok, _ := store.Add(ctx, "leader", []byte("node-a"), 30*time.Second)
if !ok { /* someone else is leader */ }

// Atomic get-and-delete
val, err := store.Pull(ctx, "one-shot-token")

// Existence
if has, _ := store.Has(ctx, "session"); has { ... }

// Bulk clear (use sparingly — affects all clients of the store)
_ = store.Flush(ctx)
```

### Distributed locks

`Lock` / `Locker` is the cache-adjacent primitive for at-most-once
execution. The interface is intentionally separate from `Store` —
backends can implement them together (Redis can serve both) or apart.

```go
locker := cache.NewMemoryLocker()

// Acquire, run, release — error returned for fn failures or if the
// lock couldn't be acquired (ErrLockNotAcquired):
err := cache.WithLock(ctx, locker, "send-newsletter", 30*time.Second,
    func(ctx context.Context) error {
        return mailer.SendNewsletter(ctx)
    })

// Block-with-timeout variant — wait up to maxWait for the lock:
err := cache.WithLockWait(ctx, locker, "report-build", 5*time.Minute, 10*time.Second,
    func(ctx context.Context) error {
        return reports.Rebuild(ctx)
    })
```

Each acquired `Lock` gets a unique random owner token, so `Release()`
only releases the holder's lock — even if the TTL expired and another
process took over in the meantime, our `Release()` is a no-op rather
than stealing the new holder's lock. (Tested in `TestMemoryLocker_ReleaseDoesNotStealOthersLock`.)

### Testing

`MemoryStore` is fully in-memory, race-safe, and uses the same Store
contract as any production backend. Swap it in for tests; behavior
matches what you'd get from a real cache:

```go
func TestUserCachedFetch(t *testing.T) {
    store := cache.NewMemoryStoreWithCleanup(0) // disable sweeper for determinism
    svc := NewUserService(repo, store)
    _, _ = svc.Get(ctx, 42) // first call hits repo + caches
    _, _ = svc.Get(ctx, 42) // second call hits cache
    if repo.Calls != 1 { t.Errorf("cache miss on second call") }
}
```

### What's intentionally NOT in `cache`

- **Cloud / network backends (Redis, Valkey, Memcached)**: implementing
  `Store` against `redis/go-redis/v9`, `redis/rueidis`, or `bradfitz/gomemcache`
  is a few hundred lines following the same pattern as `MemoryStore`.
  We don't include them in-tree because team preferences vary and we'd
  rather not pin a dependency choice.
- **Tagged caches** (`Cache::tags(['users'])->put(...)`): adds
  significant complexity (secondary indexes for tag → keys), and
  invalidation patterns vary widely. Defer until there's a concrete need.
- **Bulk Many / PutMany operations**: the `Store` interface is
  single-key for clarity. Bulk patterns can be wrapped in your own
  helper, or the underlying client used directly when you need pipelining.
- **Cache stampede protection beyond Locker**: `WithLock` + re-check
  inside the lock is the standard pattern; we don't bake a more elaborate
  scheme (singleflight, request coalescing) into `Remember` because
  the right answer depends on your tolerance for stale data.

---

## `helpers`

A grab-bag of scalar and control-flow utilities, with three subpackages
for richer domains. The aim is the most useful 80% of typical helper
collections, ported to idiomatic generics-first Go — no reflection where
type parameters do the job.

### Root: `helpers`

```go
import "github.com/emitwave/gokit/helpers"

// Lazy default
def := helpers.Value(func() *Config { return loadConfig() })

// "Blank" / "filled" checks (nil, empty string/slice/map, zero time.Time)
if helpers.Blank(input) { ... }
if helpers.Filled(token) { ... }

// Inline side effect; returns the value unchanged
user := helpers.Tap(NewUser(), func(u *User) { u.SetDefaults() })

// Lazy ternary — only the chosen branch is evaluated
title := helpers.WhenLazy(isAdmin,
    func() string { return loadAdminTitle() },
    func() string { return "Welcome" },
)

// Retry with backoff and ctx cancellation
err := helpers.Retry(ctx, 3, 200*time.Millisecond, func() error {
    return uploadFile(path)
})

// Recover from panic, return zero value, hand the error to a callback
result := helpers.Rescue(func() Order { return mustLoadOrder(id) },
    func(err error) { log.Error("order load", "err", err) })

// Assertion-style guards
if err := helpers.ThrowIf(amount < 0, errInvalidAmount); err != nil { return err }

// Typed env vars
debug := helpers.Env("DEBUG", false)
port  := helpers.Env("PORT", 8080)
name  := helpers.Env("APP_NAME", "emitwave")

// Memoize an expensive call
getCfg := helpers.OnceFn(func() *Config { return loadConfig() })

// Replaceable clock (for tests)
helpers.SetClock(func() time.Time { return fixedTime })
```

### `helpers/arr` — slice and map utilities

```go
import "github.com/emitwave/gokit/helpers/arr"

// Slice helpers (typed, generic)
nums := []int{1, 2, 3, 4, 5}
arr.First(nums, func(n int) bool { return n > 3 })  // 4, true
arr.Map(nums, func(n int) int { return n * 2 })     // [2 4 6 8 10]
arr.Where(nums, func(n int) bool { return n%2 == 0 })  // [2 4]
pass, fail := arr.Partition(nums, func(n int) bool { return n > 2 })

users := []User{{Name: "Tunde"}, {Name: "Adaeze"}}
arr.KeyBy(users, func(u User) string { return u.Name })  // map keyed by name
arr.Pluck(users, func(u User) string { return u.Name })  // ["Tunde", "Adaeze"]

arr.Take(nums, 2)   // [1 2]
arr.Take(nums, -2)  // [4 5]   (negative = last N)

arr.Join([]string{"a", "b", "c"}, ", ", " and ")  // "a, b and c"
arr.Sole(users, func(u User) bool { return u.Name == "Tunde" })  // (User, error)

// Map helpers — dot-notation paths over map[string]any (decoded JSON, configs)
m := map[string]any{}
arr.Set(m, "user.profile.email", "x@y.io")
arr.Get(m, "user.profile.email", "")          // "x@y.io"
arr.Has(m, "user.profile.email")              // true
arr.Forget(m, "user.profile.email")
arr.Pull(m, "user.profile.token", nil)        // get-and-remove

arr.Only(m, "user", "config")     // pick keys
arr.Except(m, "secret")           // drop keys

arr.Dot(m)                        // flatten to dot-notation keys
arr.Undot(flat)                   // inverse

// CSS helpers for HTML rendering
arr.ToCssClasses(map[string]bool{
    "btn":         true,
    "btn-primary": isPrimary,
    "is-loading":  loading,
})  // "btn btn-primary" (or whichever are true, sorted)
```

### `helpers/num` — number formatting

```go
import "github.com/emitwave/gokit/helpers/num"

num.Format(1234567.89, 2)        // "1,234,567.89"
num.Trim(1.2300)                  // "1.23"
num.Currency(1500, "NGN")         // "₦1,500.00"
num.Currency(1500, "JPY")         // "¥1,500"   (zero-decimal)
num.FileSize(1500*1024, 1)        // "1.5 MB"
num.Abbreviate(2_500_000, 1)      // "2.5M"
num.ForHumans(1_000_000_000, 0)   // "1 billion"
num.Ordinal(23)                   // "23rd"
num.SpellOrdinal(3)               // "third"
num.Percentage(42.5, 1)           // "42.5%"
num.Clamp(99, 0, 10)              // 10  (generic over numeric types)
num.ParseInt("42", 10, -1)        // 42  (returns -1 on failure)
num.ParseFloat("1,234.56", 0)     // 1234.56  (handles thousands separators)

// Defaults you can change app-wide
num.SetDefaultCurrency("NGN")
num.WithCurrency("EUR", func() {
    fmt.Println(num.Currency(99.99, ""))  // "€99.99"
})
```

### `helpers/data` — path-based access with wildcards

```go
import "github.com/emitwave/gokit/helpers/data"

target := map[string]any{
    "products": []any{
        map[string]any{"name": "shirt", "price": 25.0},
        map[string]any{"name": "pants", "price": 40.0},
    },
}

data.Get(target, "products.0.name", "")     // "shirt"
data.Get(target, "products.*.name", []any{}) // []any{"shirt", "pants"}
data.Set(target, "config.timeout", 30)
data.Fill(target, "config.retries", 3)       // only sets if not already set
data.Forget(target, "products.0")

data.Head([]int{1, 2, 3}, -1)  // 1
data.Last([]int{1, 2, 3}, -1)  // 3
```

### What's intentionally NOT in `helpers`

A handful of Laravel helpers don't translate to Go or duplicate parts
of this kit — they're omitted on purpose. Use the listed alternative:

| Laravel helper | What to use instead |
|---|---|
| `auth`, `policy`, `bcrypt` | A dedicated auth package + `golang.org/x/crypto/bcrypt` |
| `encrypt`, `decrypt` | `crypto/aes` + `crypto/cipher` from stdlib |
| `request`, `response`, `redirect`, `back` | `gokit/router` + `gokit/response` |
| `session` | `gokit/session` |
| `logger`, `info` | `gokit/logger` |
| `validator` | `gokit/validation` |
| `dispatch`, `dispatch_sync` | `gokit/queue` |
| `event` | `gokit/events` |
| `cache`, `broadcast`, `view`, `fake` | Out of scope (separate packages) |
| `app`, `resolve`, `config` | Not Go-idiomatic (no service container — pass dependencies explicitly) |
| `app_path`, `base_path`, `storage_path`, ... | Use `os.Executable()`, `runtime.Caller(0)`, or a config-driven base path |
| `route`, `url`, `asset`, `secure_url` | `gokit/router` (`URL(name, params)`) — assets just use a CDN/base URL var |
| `csrf_field`, `csrf_token` | Would need a dedicated CSRF package (not yet built) |
| `class_uses_recursive`, `trait_uses_recursive` | PHP-specific, no Go equivalent |
| `optional`, `Arr::accessible` | Go's static types and `nil` checks already cover this |
| `Arr::push` | Just use `append` from stdlib |
| `mapSpread` | No clean equivalent without PHP's argument unpacking |

---

## Tests

```bash
make test       # all packages
make test-race  # race detector
make cover      # coverage.html
```

## Roadmap

- [ ] Redis backend for `queue` and `session`
- [ ] Database / repository helpers (transactions, soft deletes, scopes)
- [ ] Cache abstraction (Memory / Redis)
- [ ] HTTP client with retries and circuit breaker
- [ ] Mail / SMS channel implementations for `notifications` (SMTP, SES, Twilio)
- [ ] CLI scaffold (cobra-based) for project management commands
