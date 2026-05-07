package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/emitwave/gokit/validation"
)

// CreateUserRequest demonstrates struct-tag validation. The validate tags
// are go-playground/validator syntax — comma-separated, with params after
// "=". Field names in the error bag use the json tag.
type CreateUserRequest struct {
	Name  string `json:"name"  validate:"required,max=255"`
	Email string `json:"email" validate:"required,email"`
	Age   int    `json:"age"   validate:"required,gte=18,lte=120"`
}

// handleStructValidation shows the typed-input path: decode JSON into
// a struct, then call ValidateStruct.
func handleStructValidation(w http.ResponseWriter, r *http.Request) {
	var req CreateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if errs := validation.ValidateStruct(req); errs.Any() {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"message": "validation failed",
			"errors":  errs,
		})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "user": req})
}

// handleMapValidation shows the map-based path: decode JSON
// into map[string]any, then declare rules inline. Useful for endpoints
// where the schema is dynamic, or for quick prototypes before you carve
// out a request struct.
func handleMapValidation(w http.ResponseWriter, r *http.Request) {
	var data map[string]any
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	rules := validation.Rules{
		"email":    "required|email",
		"password": "required|string|min:8|confirmed",
		"role":     "required|in:admin,user,guest",
		"age":      "required|integer|between:18,120",
		"website":  "nullable|url",
	}

	if errs := validation.Make(data, rules); errs.Any() {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"message": "validation failed",
			"errors":  errs,
		})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func main() {
	// Register a custom rule globally — available to any Make() call.
	validation.RegisterWithMessage(
		"phone_ng",
		"The :attribute must be a valid Nigerian phone number.",
		func(field string, value any, params []string, data map[string]any) (bool, map[string]string) {
			s, ok := value.(string)
			if !ok {
				return false, nil
			}
			// Very loose check — replace with a real regex in production.
			return len(s) >= 11 && (s[0] == '0' || s[0] == '+'), nil
		},
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/users/struct", handleStructValidation)
	mux.HandleFunc("/users/map", handleMapValidation)

	fmt.Println("listening on :8080")
	_ = http.ListenAndServe(":8080", mux)
}
