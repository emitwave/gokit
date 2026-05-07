// Package arr ports Laravel's Arr::* helpers to idiomatic Go. Generic
// versions are used wherever the type can be tracked through the call
// site; functions that need PHP-style mixed-key access work on
// map[string]any (the closest Go analog to PHP's associative arrays).
//
// Helpers come in two flavors:
//
//   - Slice helpers (First, Last, Map, Where, KeyBy, ...) — work on []T
//     with full type safety.
//   - Map helpers (Get, Set, Has, Forget, ...) — work on map[string]any
//     with dot-notation paths for nested access.
//
// A few Laravel methods are intentionally omitted because they don't
// translate or duplicate stdlib operations (Arr::push is just `append`,
// Arr::accessible isn't meaningful in a typed language, Arr::from
// requires PHP's mixed casting). Others (mapSpread) rely on PHP's
// argument unpacking which Go doesn't have a clean equivalent for.
package arr

import (
	"crypto/rand"
	"errors"
	"math/big"
	"net/url"
	"reflect"
	"sort"
	"strings"
)

// =========================================================================
// SLICE HELPERS — generic, type-safe operations on []T
// =========================================================================

// First returns the first element matching pred (or just the first
// element if pred is nil), and true if a match was found. Returns the
// zero value of T and false otherwise.
func First[T any](s []T, pred func(T) bool) (T, bool) {
	for _, v := range s {
		if pred == nil || pred(v) {
			return v, true
		}
	}
	var zero T
	return zero, false
}

// FirstOr returns the first element matching pred, or def if none match.
func FirstOr[T any](s []T, pred func(T) bool, def T) T {
	if v, ok := First(s, pred); ok {
		return v
	}
	return def
}

// Last returns the last element matching pred (or just the last element
// if pred is nil), and true if a match was found.
func Last[T any](s []T, pred func(T) bool) (T, bool) {
	for i := len(s) - 1; i >= 0; i-- {
		if pred == nil || pred(s[i]) {
			return s[i], true
		}
	}
	var zero T
	return zero, false
}

// LastOr returns the last element matching pred, or def if none match.
func LastOr[T any](s []T, pred func(T) bool, def T) T {
	if v, ok := Last(s, pred); ok {
		return v
	}
	return def
}

// Map applies fn to each element of s and returns a new slice of the
// results.
func Map[T, R any](s []T, fn func(T) R) []R {
	out := make([]R, len(s))
	for i, v := range s {
		out[i] = fn(v)
	}
	return out
}

// Where returns elements of s for which pred returns true. Equivalent
// to slices.Filter (when that lands in stdlib) or PHP's Arr::where.
func Where[T any](s []T, pred func(T) bool) []T {
	out := make([]T, 0, len(s))
	for _, v := range s {
		if pred(v) {
			out = append(out, v)
		}
	}
	return out
}

// Reject is the inverse of Where — keep elements for which pred returns
// false.
func Reject[T any](s []T, pred func(T) bool) []T {
	return Where(s, func(v T) bool { return !pred(v) })
}

// Every reports whether all elements satisfy pred. True for empty slices
// (vacuously true).
func Every[T any](s []T, pred func(T) bool) bool {
	for _, v := range s {
		if !pred(v) {
			return false
		}
	}
	return true
}

// Some reports whether at least one element satisfies pred. False for
// empty slices.
func Some[T any](s []T, pred func(T) bool) bool {
	for _, v := range s {
		if pred(v) {
			return true
		}
	}
	return false
}

// Partition splits s into two slices: elements satisfying pred (first
// return) and the rest (second return).
func Partition[T any](s []T, pred func(T) bool) (pass, fail []T) {
	pass = make([]T, 0, len(s))
	fail = make([]T, 0, len(s))
	for _, v := range s {
		if pred(v) {
			pass = append(pass, v)
		} else {
			fail = append(fail, v)
		}
	}
	return pass, fail
}

// KeyBy keys a slice of elements by the result of keyFn. If multiple
// elements produce the same key, later elements overwrite earlier ones.
func KeyBy[T any, K comparable](s []T, keyFn func(T) K) map[K]T {
	out := make(map[K]T, len(s))
	for _, v := range s {
		out[keyFn(v)] = v
	}
	return out
}

// Pluck extracts a single field from each element of s using a
// projection. The Laravel signature `pluck($items, "name")` becomes
// `Pluck(items, func(u User) string { return u.Name })` — typed and no
// reflection.
func Pluck[T any, R any](s []T, fn func(T) R) []R {
	return Map(s, fn)
}

// Sole returns the only element matching pred, or an error if zero or
// more than one match.
func Sole[T any](s []T, pred func(T) bool) (T, error) {
	var zero, found T
	count := 0
	for _, v := range s {
		if pred == nil || pred(v) {
			found = v
			count++
			if count > 1 {
				return zero, ErrMultipleMatches
			}
		}
	}
	if count == 0 {
		return zero, ErrNoMatch
	}
	return found, nil
}

// ErrNoMatch is returned by Sole when no element matches.
var ErrNoMatch = errors.New("arr: no element matched")

// ErrMultipleMatches is returned by Sole when more than one element matches.
var ErrMultipleMatches = errors.New("arr: more than one element matched")

// Take returns the first n elements of s. Negative n returns the LAST n
// elements. If n is larger than len(s), returns the whole slice.
func Take[T any](s []T, n int) []T {
	if n >= 0 {
		if n > len(s) {
			n = len(s)
		}
		return append([]T(nil), s[:n]...)
	}
	// negative — last |n|
	if -n >= len(s) {
		return append([]T(nil), s...)
	}
	return append([]T(nil), s[len(s)+n:]...)
}

// Wrap returns []T{v} unless v is already a []T (in which case returns
// it as-is). Mirrors Arr::wrap. Generic version — for any-typed input,
// use WrapAny.
func Wrap[T any](v T) []T { return []T{v} }

// WrapAny mirrors Laravel's Arr::wrap exactly: if v is nil → [], if v
// is already []any → v, else → []any{v}. Useful for normalizing JSON-
// shaped inputs.
func WrapAny(v any) []any {
	if v == nil {
		return []any{}
	}
	if s, ok := v.([]any); ok {
		return s
	}
	return []any{v}
}

// Shuffle returns a new slice with the elements of s in random order.
// Uses crypto/rand for cryptographic-quality shuffling — slower than
// math/rand but means the result is suitable for things like password
// suggestion lists, not just visual variety.
func Shuffle[T any](s []T) []T {
	out := append([]T(nil), s...)
	for i := len(out) - 1; i > 0; i-- {
		j, _ := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		k := int(j.Int64())
		out[i], out[k] = out[k], out[i]
	}
	return out
}

// Random returns up to n random elements from s (without replacement).
// If n >= len(s), returns a shuffled copy of the whole slice.
func Random[T any](s []T, n int) []T {
	if n <= 0 {
		return []T{}
	}
	shuffled := Shuffle(s)
	if n > len(shuffled) {
		n = len(shuffled)
	}
	return shuffled[:n]
}

// Sort returns a new sorted slice using less. Stable.
func Sort[T any](s []T, less func(a, b T) bool) []T {
	out := append([]T(nil), s...)
	sort.SliceStable(out, func(i, j int) bool { return less(out[i], out[j]) })
	return out
}

// SortDesc returns a new sorted slice in DESCENDING order using less.
// Equivalent to Sort with the comparator inverted.
func SortDesc[T any](s []T, less func(a, b T) bool) []T {
	return Sort(s, func(a, b T) bool { return less(b, a) })
}

// Join concatenates elements of s with sep. If finalSep is non-empty
// AND len(s) > 1, the LAST joiner becomes finalSep. Useful for English
// list formatting: `"a, b, and c"` rather than `"a, b, c"`.
func Join(s []string, sep, finalSep string) string {
	if len(s) == 0 {
		return ""
	}
	if len(s) == 1 || finalSep == "" {
		return strings.Join(s, sep)
	}
	return strings.Join(s[:len(s)-1], sep) + finalSep + s[len(s)-1]
}

// Collapse flattens a slice of slices into a single slice (one level only).
func Collapse[T any](s [][]T) []T {
	total := 0
	for _, sub := range s {
		total += len(sub)
	}
	out := make([]T, 0, total)
	for _, sub := range s {
		out = append(out, sub...)
	}
	return out
}

// CrossJoin returns the Cartesian product of the input slices. The
// outer length equals the product of the inputs' lengths; each inner
// slice has one element from each input in order.
func CrossJoin[T any](inputs ...[]T) [][]T {
	result := [][]T{{}}
	for _, group := range inputs {
		next := make([][]T, 0, len(result)*len(group))
		for _, r := range result {
			for _, item := range group {
				row := make([]T, len(r)+1)
				copy(row, r)
				row[len(r)] = item
				next = append(next, row)
			}
		}
		result = next
	}
	return result
}

// =========================================================================
// MAP HELPERS — operate on map[string]any with optional dot-notation paths
// =========================================================================

// Get retrieves a value from m using a dot-notation path. Returns def if
// the path doesn't resolve. Mirrors Laravel's Arr::get / data_get.
//
//	arr.Get(m, "user.profile.name", "anonymous")
//
// Path segments traverse maps. Numeric segments index into []any slices.
func Get(m map[string]any, path string, def any) any {
	if v, ok := getPath(m, path); ok {
		return v
	}
	return def
}

// Has reports whether ALL given paths resolve in m.
func Has(m map[string]any, paths ...string) bool {
	for _, p := range paths {
		if _, ok := getPath(m, p); !ok {
			return false
		}
	}
	return true
}

// HasAny reports whether at least one of the paths resolves in m.
func HasAny(m map[string]any, paths ...string) bool {
	for _, p := range paths {
		if _, ok := getPath(m, p); ok {
			return true
		}
	}
	return false
}

// HasAll is an alias for Has — included for parity with Laravel's
// Arr::hasAll.
func HasAll(m map[string]any, paths ...string) bool { return Has(m, paths...) }

// Set assigns value at the given dot-notation path, creating intermediate
// maps as needed. Returns m for chaining. Mirrors Arr::set / data_set.
func Set(m map[string]any, path string, value any) map[string]any {
	if path == "" {
		return m
	}
	parts := strings.Split(path, ".")
	cur := m
	for i, k := range parts {
		if i == len(parts)-1 {
			cur[k] = value
			return m
		}
		next, ok := cur[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[k] = next
		}
		cur = next
	}
	return m
}

// Add sets path = value only if path is not already set. Mirrors
// Arr::add.
func Add(m map[string]any, path string, value any) map[string]any {
	if _, ok := getPath(m, path); !ok {
		Set(m, path, value)
	}
	return m
}

// Forget removes the value at each dot-notation path. Modifies m in-
// place. Mirrors Arr::forget / data_forget.
func Forget(m map[string]any, paths ...string) {
	for _, path := range paths {
		if path == "" {
			continue
		}
		parts := strings.Split(path, ".")
		cur := m
		for i, k := range parts {
			if i == len(parts)-1 {
				delete(cur, k)
				return
			}
			next, ok := cur[k].(map[string]any)
			if !ok {
				return
			}
			cur = next
		}
	}
}

// Pull retrieves the value at path and removes it from m, returning
// (value, found). If not found, returns (def, false).
func Pull(m map[string]any, path string, def any) (any, bool) {
	v, ok := getPath(m, path)
	if !ok {
		return def, false
	}
	Forget(m, path)
	return v, true
}

// Only returns a new map containing only the given top-level keys.
// (Does not descend — operates on top level only, matching Laravel.)
func Only(m map[string]any, keys ...string) map[string]any {
	out := make(map[string]any, len(keys))
	for _, k := range keys {
		if v, ok := m[k]; ok {
			out[k] = v
		}
	}
	return out
}

// Except returns a new map with the given top-level keys removed.
func Except(m map[string]any, keys ...string) map[string]any {
	skip := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		skip[k] = struct{}{}
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if _, drop := skip[k]; drop {
			continue
		}
		out[k] = v
	}
	return out
}

// Exists reports whether the top-level key exists in m. (Use Has for
// dot-notation paths.)
func Exists(m map[string]any, key string) bool {
	_, ok := m[key]
	return ok
}

// Dot flattens nested maps into a single-level map with dot-notation
// keys. Slices are indexed numerically: `users.0.name`. Mirrors
// Arr::dot.
func Dot(m map[string]any) map[string]any {
	out := map[string]any{}
	dotInto(out, m, "")
	return out
}

func dotInto(out map[string]any, v any, prefix string) {
	switch t := v.(type) {
	case map[string]any:
		if len(t) == 0 && prefix != "" {
			out[prefix] = t
			return
		}
		for k, child := range t {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			dotInto(out, child, key)
		}
	case []any:
		if len(t) == 0 && prefix != "" {
			out[prefix] = t
			return
		}
		for i, child := range t {
			key := strFromInt(i)
			if prefix != "" {
				key = prefix + "." + strFromInt(i)
			}
			dotInto(out, child, key)
		}
	default:
		out[prefix] = v
	}
}

// Undot is the inverse of Dot. Takes a flat map with dot-notation keys
// and produces a nested structure.
func Undot(m map[string]any) map[string]any {
	out := map[string]any{}
	for path, v := range m {
		Set(out, path, v)
	}
	return out
}

// Flatten flattens nested slices into a single-level []any. depth
// controls how many levels of nesting to unwrap:
//
//   - -1 (or any negative): unlimited — fully flat
//   - 0: don't unwrap (returns elements as-is, equivalent to []any conversion)
//   - 1: unwrap one level (Laravel's classic "flatten by 1")
//   - n: unwrap n levels
//
// Mirrors Laravel's Arr::flatten — `[[a,b],[c,[d]]]` with depth 1 →
// `[a, b, c, [d]]`; with depth 2 (or unlimited) → `[a, b, c, d]`.
func Flatten(v any, depth int) []any {
	out := []any{}
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array) {
		return out
	}

	for i := 0; i < rv.Len(); i++ {
		child := rv.Index(i).Interface()
		crv := reflect.ValueOf(child)
		isSlice := crv.IsValid() && (crv.Kind() == reflect.Slice || crv.Kind() == reflect.Array)

		if !isSlice || depth == 0 {
			// Not a slice OR we've used up our unwrap budget — keep as-is.
			out = append(out, child)
			continue
		}

		if depth == 1 {
			// Final unwrap — append children directly without recursion.
			for j := 0; j < crv.Len(); j++ {
				out = append(out, crv.Index(j).Interface())
			}
			continue
		}

		// depth > 1 or unlimited (depth < 0): recurse with one level used up.
		// Negative depth stays negative (unlimited keeps unlimited).
		nextDepth := depth - 1
		if depth < 0 {
			nextDepth = depth // -1 stays -1
		}
		out = append(out, Flatten(child, nextDepth)...)
	}
	return out
}

// Divide splits m into a slice of keys and a slice of corresponding
// values. The order is non-deterministic (Go map iteration); use Sort
// on the keys if you need a specific order.
func Divide(m map[string]any) (keys []string, values []any) {
	keys = make([]string, 0, len(m))
	values = make([]any, 0, len(m))
	for k, v := range m {
		keys = append(keys, k)
		values = append(values, v)
	}
	return keys, values
}

// PrependKeysWith returns a new map with prefix prepended to each key.
// Useful for namespacing config sections.
func PrependKeysWith(m map[string]any, prefix string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[prefix+k] = v
	}
	return out
}

// Query encodes m as a URL query string, using net/url for proper
// escaping. Slice values become repeated parameters: `tags[]=a&tags[]=b`
// becomes `tags=a&tags=b` here (Go convention).
func Query(m map[string]any) string {
	values := url.Values{}
	for k, v := range m {
		switch t := v.(type) {
		case string:
			values.Add(k, t)
		case []string:
			for _, s := range t {
				values.Add(k, s)
			}
		case []any:
			for _, item := range t {
				values.Add(k, anyToString(item))
			}
		default:
			values.Add(k, anyToString(v))
		}
	}
	return values.Encode()
}

// IsAssoc reports whether v is a map (Laravel-associative). For Go,
// slices and arrays are NOT associative; only maps are.
func IsAssoc(v any) bool {
	rv := reflect.ValueOf(v)
	return rv.IsValid() && rv.Kind() == reflect.Map
}

// IsList reports whether v is a sequentially-indexed list — a slice or
// array in Go terms.
func IsList(v any) bool {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return false
	}
	k := rv.Kind()
	return k == reflect.Slice || k == reflect.Array
}

// =========================================================================
// CSS helpers — handy for HTML-rendering paths
// =========================================================================

// ToCssClasses joins a class map into a space-separated class string.
// Each key is included if its value is true (or non-zero, etc., per
// Filled). Non-boolean keys (just strings as keys with index values)
// are always included.
//
//	arr.ToCssClasses(map[string]bool{
//	    "btn":         true,
//	    "btn-primary": isPrimary,
//	    "is-loading":  loading,
//	})
func ToCssClasses(classes map[string]bool) string {
	out := []string{}
	for k, on := range classes {
		if on {
			out = append(out, k)
		}
	}
	sort.Strings(out) // deterministic output
	return strings.Join(out, " ")
}

// ToCssStyles joins a style map into a "key: value;" string.
func ToCssStyles(styles map[string]string) string {
	keys := make([]string, 0, len(styles))
	for k := range styles {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(styles[k])
		b.WriteString("; ")
	}
	return strings.TrimSpace(b.String())
}

// =========================================================================
// internal path resolution
// =========================================================================

// getPath walks m by dot-notation segments. Numeric segments index
// into []any. Returns the resolved value and whether the path was found.
func getPath(m map[string]any, path string) (any, bool) {
	if path == "" {
		return m, true
	}
	var cur any = m
	for _, seg := range strings.Split(path, ".") {
		switch t := cur.(type) {
		case map[string]any:
			v, ok := t[seg]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			idx, err := strconvAtoi(seg)
			if err != nil || idx < 0 || idx >= len(t) {
				return nil, false
			}
			cur = t[idx]
		default:
			return nil, false
		}
	}
	return cur, true
}

// minimal strconv.Atoi to avoid pulling the package up here just for one
// call site. Returns err for non-digit strings.
func strconvAtoi(s string) (int, error) {
	if s == "" {
		return 0, errors.New("empty")
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("not a number")
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

func strFromInt(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	if n < 0 {
		digits = append(digits, '-')
		n = -n
	}
	tmp := []byte{}
	for n > 0 {
		tmp = append(tmp, byte('0'+n%10))
		n /= 10
	}
	for i := len(tmp) - 1; i >= 0; i-- {
		digits = append(digits, tmp[i])
	}
	return string(digits)
}

func anyToString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	case bool:
		if t {
			return "true"
		}
		return "false"
	case int, int64, int32, int16, int8, uint, uint64, uint32, uint16, uint8:
		return strFromInt(reflect.ValueOf(v).Convert(reflect.TypeOf(int(0))).Interface().(int))
	default:
		return reflect.ValueOf(v).String() // fallback; not always pretty
	}
}
