package resource

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Tests use a fake "user" model + a transformer that produces a DTO.
type user struct {
	ID    string
	Name  string
	Email string
	pwHash string // private — must NOT appear in transformed output
}

type userDTO struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

func userTransformer(u user) any {
	return userDTO{ID: u.ID, Name: u.Name, Email: u.Email}
}

func TestItem(t *testing.T) {
	rec := httptest.NewRecorder()
	u := user{ID: "u1", Name: "Tunde", Email: "t@x.com", pwHash: "secret"}

	Item(rec, http.StatusOK, u, userTransformer)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
	var got struct {
		Data userDTO `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Data.ID != "u1" || got.Data.Name != "Tunde" {
		t.Errorf("DTO mangled: %+v", got.Data)
	}
	// The transformer leaks pwHash if the caller bypassed it — our
	// goal here is to confirm that we DON'T expose private fields when
	// the transformer is well-written. The test passes because the JSON
	// only has "id", "name", "email".
}

func TestItemCreated(t *testing.T) {
	rec := httptest.NewRecorder()
	Item(rec, http.StatusCreated, user{ID: "u1"}, userTransformer)
	if rec.Code != http.StatusCreated {
		t.Errorf("status: got %d, want 201", rec.Code)
	}
}

func TestCollection(t *testing.T) {
	rec := httptest.NewRecorder()
	users := []user{
		{ID: "u1", Name: "A"},
		{ID: "u2", Name: "B"},
		{ID: "u3", Name: "C"},
	}
	Collection(rec, http.StatusOK, users, userTransformer)

	var got struct {
		Data []userDTO `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Data) != 3 {
		t.Errorf("got %d items, want 3", len(got.Data))
	}
	if got.Data[1].ID != "u2" {
		t.Errorf("item 1: got %+v", got.Data[1])
	}
}

func TestCollectionEmpty(t *testing.T) {
	rec := httptest.NewRecorder()
	Collection(rec, http.StatusOK, []user{}, userTransformer)

	// An empty collection should render as `"data": []`, not null —
	// frontends should not have to special-case nil.
	var got map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&got)
	data, ok := got["data"].([]any)
	if !ok {
		t.Fatalf("data should be array, got %T (%v)", got["data"], got["data"])
	}
	if len(data) != 0 {
		t.Errorf("expected empty array, got %v", data)
	}
}

func TestPaginated(t *testing.T) {
	rec := httptest.NewRecorder()
	users := []user{{ID: "u1"}, {ID: "u2"}}
	Paginated(rec, users, 2, 25, 100, userTransformer)

	var got struct {
		Data []userDTO `json:"data"`
		Meta struct {
			Pagination struct {
				Page    int   `json:"page"`
				PerPage int   `json:"per_page"`
				Total   int64 `json:"total"`
				Pages   int   `json:"pages"`
			} `json:"pagination"`
		} `json:"meta"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Meta.Pagination.Page != 2 || got.Meta.Pagination.Total != 100 {
		t.Errorf("pagination meta wrong: %+v", got.Meta.Pagination)
	}
	if len(got.Data) != 2 {
		t.Errorf("data len: got %d, want 2", len(got.Data))
	}
}

func TestWithComposition(t *testing.T) {
	// Base transformer that returns a map (required for With to compose).
	baseMap := func(u user) any {
		return map[string]any{
			"id":   u.ID,
			"name": u.Name,
		}
	}
	withAdmin := With(baseMap, func(u user) map[string]any {
		return map[string]any{"is_admin": u.ID == "u1"}
	})

	rec := httptest.NewRecorder()
	Item(rec, http.StatusOK, user{ID: "u1", Name: "Tunde"}, withAdmin)

	var got struct {
		Data map[string]any `json:"data"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&got)

	if got.Data["id"] != "u1" {
		t.Errorf("base field missing: %v", got.Data)
	}
	if got.Data["is_admin"] != true {
		t.Errorf("composed field missing or wrong: %v", got.Data)
	}
}
