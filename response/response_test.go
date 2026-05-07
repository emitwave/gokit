package response

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emitwave/gokit/validation"
)

func TestOK(t *testing.T) {
	rec := httptest.NewRecorder()
	OK(rec, map[string]string{"name": "Tunde"})

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("content-type: got %q", ct)
	}

	var got SuccessEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Data == nil {
		t.Errorf("data should be present")
	}
	if got.Meta != nil {
		t.Errorf("meta should be omitted when empty, got %v", got.Meta)
	}
}

func TestOKWithMeta(t *testing.T) {
	rec := httptest.NewRecorder()
	OKWithMeta(rec, []int{1, 2, 3}, map[string]any{"count": 3})

	var got map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&got)

	meta, ok := got["meta"].(map[string]any)
	if !ok {
		t.Fatalf("meta missing or wrong type: %v", got)
	}
	if meta["count"] != float64(3) { // JSON numbers decode as float64
		t.Errorf("meta.count: got %v, want 3", meta["count"])
	}
}

func TestNoContent(t *testing.T) {
	rec := httptest.NewRecorder()
	NoContent(rec)
	if rec.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want 204", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body should be empty, got %q", rec.Body.String())
	}
}

func TestErrorEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/x", nil)

	NotFound(rec, r, "")

	if rec.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rec.Code)
	}
	var got ErrorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Error.Code != CodeNotFound {
		t.Errorf("code: got %q, want %q", got.Error.Code, CodeNotFound)
	}
	if got.Error.Message == "" {
		t.Error("default message should be applied")
	}
}

func TestErrorWithCustomMessage(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/x", nil)

	NotFound(rec, r, "User not found.")

	var got ErrorEnvelope
	_ = json.NewDecoder(rec.Body).Decode(&got)
	if got.Error.Message != "User not found." {
		t.Errorf("custom message dropped: got %q", got.Error.Message)
	}
}

func TestValidationFailed(t *testing.T) {
	errs := validation.NewErrors()
	errs.Add("email", "The email field is required.")
	errs.Add("password", "The password must be at least 8 characters.")

	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/x", nil)
	ValidationFailed(rec, r, errs)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status: got %d, want 422", rec.Code)
	}

	var got struct {
		Error struct {
			Code    string              `json:"code"`
			Details map[string][]string `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Error.Code != CodeValidationFailed {
		t.Errorf("code: got %q", got.Error.Code)
	}
	if len(got.Error.Details["email"]) != 1 {
		t.Errorf("expected 1 email error, got %v", got.Error.Details)
	}
}

func TestPaginated(t *testing.T) {
	rec := httptest.NewRecorder()
	data := []map[string]any{{"id": 1}, {"id": 2}}
	Paginated(rec, data, 2, 25, 100)

	var got struct {
		Data []map[string]any `json:"data"`
		Meta struct {
			Pagination PageMeta `json:"pagination"`
		} `json:"meta"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	pm := got.Meta.Pagination
	if pm.Page != 2 || pm.PerPage != 25 || pm.Total != 100 || pm.Pages != 4 {
		t.Errorf("pagination meta wrong: %+v", pm)
	}
}

func TestPageQuery(t *testing.T) {
	tests := []struct {
		url             string
		wantPage        int
		wantPerPage     int
		defaultPerPage  int
		maxPerPage      int
	}{
		{"/x", 1, 20, 20, 100},                 // defaults
		{"/x?page=3", 3, 20, 20, 100},          // explicit page
		{"/x?page=-1", 1, 20, 20, 100},         // negative clamped
		{"/x?per_page=500", 1, 100, 20, 100},   // max clamped
		{"/x?page=2&per_page=10", 2, 10, 20, 100},
	}
	for _, tt := range tests {
		r := httptest.NewRequest("GET", tt.url, nil)
		page, perPage := PageQuery(r, tt.defaultPerPage, tt.maxPerPage)
		if page != tt.wantPage || perPage != tt.wantPerPage {
			t.Errorf("%s: got (%d, %d), want (%d, %d)",
				tt.url, page, perPage, tt.wantPage, tt.wantPerPage)
		}
	}
}
