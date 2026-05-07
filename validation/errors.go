package validation

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Errors is a field-keyed error bag that collects validation failures
// keyed by field name. A field can have multiple errors (e.g. an email
// can fail both "required" and "email" if you bypass short-circuiting).
type Errors struct {
	bag map[string][]string
}

// NewErrors returns an empty error bag.
func NewErrors() *Errors {
	return &Errors{bag: make(map[string][]string)}
}

// Add appends a message under the given field.
func (e *Errors) Add(field, message string) {
	if e.bag == nil {
		e.bag = make(map[string][]string)
	}
	e.bag[field] = append(e.bag[field], message)
}

// Any reports whether the bag contains any errors.
func (e *Errors) Any() bool {
	return e != nil && len(e.bag) > 0
}

// Has reports whether a specific field has any errors.
func (e *Errors) Has(field string) bool {
	if e == nil {
		return false
	}
	_, ok := e.bag[field]
	return ok
}

// Get returns all error messages for a field, or nil if none.
func (e *Errors) Get(field string) []string {
	if e == nil {
		return nil
	}
	return e.bag[field]
}

// First returns the first error message for a field, or "" if none.
func (e *Errors) First(field string) string {
	msgs := e.Get(field)
	if len(msgs) == 0 {
		return ""
	}
	return msgs[0]
}

// All returns the full map of fields to messages.
func (e *Errors) All() map[string][]string {
	if e == nil {
		return nil
	}
	// Return a copy so callers can't mutate internal state.
	out := make(map[string][]string, len(e.bag))
	for k, v := range e.bag {
		cp := make([]string, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}

// Count returns the total number of error messages across all fields.
func (e *Errors) Count() int {
	if e == nil {
		return 0
	}
	n := 0
	for _, v := range e.bag {
		n += len(v)
	}
	return n
}

// Error implements the error interface so *Errors can be returned as error.
// It produces a compact, human-readable summary; use MarshalJSON for APIs.
func (e *Errors) Error() string {
	if !e.Any() {
		return ""
	}
	parts := make([]string, 0, len(e.bag))
	for field, msgs := range e.bag {
		parts = append(parts, fmt.Sprintf("%s: %s", field, strings.Join(msgs, ", ")))
	}
	return "validation failed: " + strings.Join(parts, "; ")
}

// MarshalJSON renders the bag as {"field":["msg1","msg2"]} — the shape
// most frontends already expect for field-keyed validation errors.
func (e *Errors) MarshalJSON() ([]byte, error) {
	if e == nil || e.bag == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(e.bag)
}
