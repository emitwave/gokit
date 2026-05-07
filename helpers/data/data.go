// Package data ports Laravel's data_* helpers and the head/last shortcuts.
//
// These helpers operate on nested map[string]any / []any structures —
// the typical shape of decoded JSON, configuration trees, or any "free-
// form" data. Path segments are dot-separated, with `*` as a wildcard
// to map over slice/map values:
//
//	data.Get(target, "products.0.name", "")
//	data.Get(target, "products.*.price", []float64{})
//	data.Set(target, "user.profile.email", "x@y.io")
//	data.Fill(target, "user.preferences.theme", "dark") // only if not set
//	data.Forget(target, "user.tokens")
//
// For most uses, the arr.Get/Set/Has/Forget functions cover non-wildcard
// access more cleanly. data_* exists as the wildcard-capable cousin
// when you need it.
package data

import (
	"reflect"
	"strconv"
	"strings"
)

// Get retrieves a value from target using a dot-notation path. Numeric
// segments index into slices; the wildcard `*` maps over a slice or
// map's values, returning a []any. Returns def if the path doesn't
// resolve.
//
//	data.Get(target, "users.0.name", "")
//	data.Get(target, "users.*.name", []string{})  // returns []any of names
func Get(target any, path string, def any) any {
	if path == "" {
		return target
	}
	v, ok := getPath(target, strings.Split(path, "."))
	if !ok {
		return def
	}
	return v
}

// Set assigns value at the dot-notation path on target (a map[string]any
// or *map[string]any). Creates intermediate maps as needed. Returns
// true if the assignment succeeded.
//
// data.Set on a slice/array element requires the slice be addressable
// — pass a pointer to the slice in your map structure for that to work.
func Set(target map[string]any, path string, value any) bool {
	if target == nil || path == "" {
		return false
	}
	parts := strings.Split(path, ".")
	cur := target
	for i, k := range parts {
		if i == len(parts)-1 {
			cur[k] = value
			return true
		}
		next, ok := cur[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[k] = next
		}
		cur = next
	}
	return true
}

// Fill is Set but only assigns if the path is currently unset.
//
//	data.Fill(target, "config.timeout", 30) // sets only if config.timeout doesn't exist
func Fill(target map[string]any, path string, value any) bool {
	if _, ok := getPath(target, strings.Split(path, ".")); ok {
		return false
	}
	return Set(target, path, value)
}

// Forget removes the value at path. Returns true if anything was removed.
func Forget(target map[string]any, path string) bool {
	if target == nil || path == "" {
		return false
	}
	parts := strings.Split(path, ".")
	cur := target
	for i, k := range parts {
		if i == len(parts)-1 {
			if _, ok := cur[k]; !ok {
				return false
			}
			delete(cur, k)
			return true
		}
		next, ok := cur[k].(map[string]any)
		if !ok {
			return false
		}
		cur = next
	}
	return false
}

// Head returns the first element of a slice. Returns def if the input
// is empty or not a slice.
func Head[T any](s []T, def T) T {
	if len(s) == 0 {
		return def
	}
	return s[0]
}

// Last returns the last element of a slice. Returns def if empty.
func Last[T any](s []T, def T) T {
	if len(s) == 0 {
		return def
	}
	return s[len(s)-1]
}

// =========================================================================
// path resolution with wildcard support
// =========================================================================

func getPath(target any, parts []string) (any, bool) {
	cur := target
	for i, seg := range parts {
		// Wildcard segment — map over current value's elements.
		if seg == "*" {
			rest := parts[i+1:]
			return wildcardCollect(cur, rest)
		}

		switch t := cur.(type) {
		case map[string]any:
			v, ok := t[seg]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(t) {
				return nil, false
			}
			cur = t[idx]
		default:
			// Try reflection for typed slices/maps (e.g. []User).
			rv := reflect.ValueOf(cur)
			if !rv.IsValid() {
				return nil, false
			}
			switch rv.Kind() {
			case reflect.Slice, reflect.Array:
				idx, err := strconv.Atoi(seg)
				if err != nil || idx < 0 || idx >= rv.Len() {
					return nil, false
				}
				cur = rv.Index(idx).Interface()
			case reflect.Map:
				// Only string-keyed maps are supported via path traversal.
				key := reflect.ValueOf(seg)
				if rv.Type().Key().Kind() != reflect.String {
					return nil, false
				}
				v := rv.MapIndex(key)
				if !v.IsValid() {
					return nil, false
				}
				cur = v.Interface()
			default:
				return nil, false
			}
		}
	}
	return cur, true
}

// wildcardCollect maps over the current container's elements and
// resolves the rest of the path on each. The result is always a []any.
func wildcardCollect(cur any, rest []string) (any, bool) {
	out := []any{}
	switch t := cur.(type) {
	case []any:
		for _, item := range t {
			if len(rest) == 0 {
				out = append(out, item)
				continue
			}
			if v, ok := getPath(item, rest); ok {
				out = append(out, v)
			}
		}
		return out, true
	case map[string]any:
		for _, item := range t {
			if len(rest) == 0 {
				out = append(out, item)
				continue
			}
			if v, ok := getPath(item, rest); ok {
				out = append(out, v)
			}
		}
		return out, true
	}
	// Reflection fallback for typed slices/maps.
	rv := reflect.ValueOf(cur)
	if !rv.IsValid() {
		return out, true
	}
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		for i := 0; i < rv.Len(); i++ {
			item := rv.Index(i).Interface()
			if len(rest) == 0 {
				out = append(out, item)
				continue
			}
			if v, ok := getPath(item, rest); ok {
				out = append(out, v)
			}
		}
		return out, true
	case reflect.Map:
		for _, key := range rv.MapKeys() {
			item := rv.MapIndex(key).Interface()
			if len(rest) == 0 {
				out = append(out, item)
				continue
			}
			if v, ok := getPath(item, rest); ok {
				out = append(out, v)
			}
		}
		return out, true
	}
	return out, true
}
