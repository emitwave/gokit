package arr

import (
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// =========================================================================
// Slice helpers
// =========================================================================

func TestFirstAndLast(t *testing.T) {
	s := []int{1, 2, 3, 4}

	if v, ok := First(s, nil); !ok || v != 1 {
		t.Errorf("First(nil pred): %v %v", v, ok)
	}
	if v, ok := First(s, func(x int) bool { return x > 2 }); !ok || v != 3 {
		t.Errorf("First(>2): %v %v", v, ok)
	}
	if _, ok := First(s, func(x int) bool { return x > 99 }); ok {
		t.Errorf("First: should not match")
	}
	if v := FirstOr(s, func(x int) bool { return x > 99 }, -1); v != -1 {
		t.Errorf("FirstOr default: got %d", v)
	}

	if v, ok := Last(s, nil); !ok || v != 4 {
		t.Errorf("Last(nil pred): %v %v", v, ok)
	}
	if v, ok := Last(s, func(x int) bool { return x < 3 }); !ok || v != 2 {
		t.Errorf("Last(<3): %v %v", v, ok)
	}
}

func TestMap(t *testing.T) {
	got := Map([]int{1, 2, 3}, func(x int) int { return x * 2 })
	want := []int{2, 4, 6}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}

	// Different output type
	gotStr := Map([]int{1, 2}, func(x int) string {
		return strFromInt(x)
	})
	if !reflect.DeepEqual(gotStr, []string{"1", "2"}) {
		t.Errorf("got %v", gotStr)
	}
}

func TestWhereAndReject(t *testing.T) {
	s := []int{1, 2, 3, 4, 5}
	w := Where(s, func(x int) bool { return x%2 == 0 })
	if !reflect.DeepEqual(w, []int{2, 4}) {
		t.Errorf("Where: %v", w)
	}
	r := Reject(s, func(x int) bool { return x%2 == 0 })
	if !reflect.DeepEqual(r, []int{1, 3, 5}) {
		t.Errorf("Reject: %v", r)
	}
}

func TestEveryAndSome(t *testing.T) {
	pos := []int{1, 2, 3}
	mix := []int{1, -2, 3}

	if !Every(pos, func(x int) bool { return x > 0 }) {
		t.Error("Every pos>0")
	}
	if Every(mix, func(x int) bool { return x > 0 }) {
		t.Error("Every mix>0 should be false")
	}
	if !Every([]int{}, func(x int) bool { return false }) {
		t.Error("Every on empty should be true (vacuously)")
	}

	if !Some(mix, func(x int) bool { return x < 0 }) {
		t.Error("Some <0")
	}
	if Some(pos, func(x int) bool { return x < 0 }) {
		t.Error("Some pos<0 should be false")
	}
}

func TestPartition(t *testing.T) {
	s := []int{1, 2, 3, 4, 5}
	pass, fail := Partition(s, func(x int) bool { return x%2 == 0 })
	if !reflect.DeepEqual(pass, []int{2, 4}) {
		t.Errorf("pass: %v", pass)
	}
	if !reflect.DeepEqual(fail, []int{1, 3, 5}) {
		t.Errorf("fail: %v", fail)
	}
}

type user struct {
	Name string
	Age  int
}

func TestKeyByAndPluck(t *testing.T) {
	users := []user{
		{"Tunde", 30},
		{"Adaeze", 28},
	}
	byName := KeyBy(users, func(u user) string { return u.Name })
	if byName["Tunde"].Age != 30 || byName["Adaeze"].Age != 28 {
		t.Errorf("KeyBy: %+v", byName)
	}

	names := Pluck(users, func(u user) string { return u.Name })
	sort.Strings(names)
	if !reflect.DeepEqual(names, []string{"Adaeze", "Tunde"}) {
		t.Errorf("Pluck: %v", names)
	}
}

func TestSole(t *testing.T) {
	s := []int{1, 2, 3, 4}
	v, err := Sole(s, func(x int) bool { return x == 3 })
	if err != nil || v != 3 {
		t.Errorf("Sole(==3): v=%d err=%v", v, err)
	}

	if _, err := Sole(s, func(x int) bool { return x > 99 }); !errors.Is(err, ErrNoMatch) {
		t.Errorf("Sole(no match): %v", err)
	}
	if _, err := Sole(s, func(x int) bool { return x > 1 }); !errors.Is(err, ErrMultipleMatches) {
		t.Errorf("Sole(many): %v", err)
	}
}

func TestTake(t *testing.T) {
	s := []int{1, 2, 3, 4, 5}
	if got := Take(s, 2); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Errorf("first 2: %v", got)
	}
	if got := Take(s, -2); !reflect.DeepEqual(got, []int{4, 5}) {
		t.Errorf("last 2: %v", got)
	}
	if got := Take(s, 99); !reflect.DeepEqual(got, []int{1, 2, 3, 4, 5}) {
		t.Errorf("over: %v", got)
	}
}

func TestWrap(t *testing.T) {
	if got := Wrap(7); !reflect.DeepEqual(got, []int{7}) {
		t.Errorf("Wrap(7): %v", got)
	}
}

func TestWrapAny(t *testing.T) {
	if got := WrapAny(nil); len(got) != 0 {
		t.Errorf("WrapAny(nil): %v", got)
	}
	if got := WrapAny("x"); !reflect.DeepEqual(got, []any{"x"}) {
		t.Errorf("WrapAny(scalar): %v", got)
	}
	in := []any{"a", "b"}
	if got := WrapAny(in); !reflect.DeepEqual(got, in) {
		t.Errorf("WrapAny(slice): %v", got)
	}
}

func TestShuffleAndRandom(t *testing.T) {
	s := []int{1, 2, 3, 4, 5, 6, 7, 8}
	out := Shuffle(s)
	// Cannot assert exact order, only that all elements are present.
	if len(out) != len(s) {
		t.Errorf("shuffled length: %d", len(out))
	}
	sortedOut := append([]int(nil), out...)
	sort.Ints(sortedOut)
	if !reflect.DeepEqual(sortedOut, s) {
		t.Errorf("shuffled drops elements: %v vs %v", sortedOut, s)
	}

	if got := Random(s, 3); len(got) != 3 {
		t.Errorf("Random(3) len: %d", len(got))
	}
	if got := Random(s, 0); len(got) != 0 {
		t.Errorf("Random(0): %v", got)
	}
	if got := Random(s, 99); len(got) != len(s) {
		t.Errorf("Random(>n): %d", len(got))
	}
}

func TestSortAndSortDesc(t *testing.T) {
	s := []int{3, 1, 4, 1, 5, 9, 2, 6}
	asc := Sort(s, func(a, b int) bool { return a < b })
	want := []int{1, 1, 2, 3, 4, 5, 6, 9}
	if !reflect.DeepEqual(asc, want) {
		t.Errorf("Sort: %v", asc)
	}
	desc := SortDesc(s, func(a, b int) bool { return a < b })
	wantDesc := []int{9, 6, 5, 4, 3, 2, 1, 1}
	if !reflect.DeepEqual(desc, wantDesc) {
		t.Errorf("SortDesc: %v", desc)
	}
}

func TestJoin(t *testing.T) {
	cases := []struct {
		in       []string
		sep      string
		finalSep string
		want     string
	}{
		{[]string{}, ", ", " and ", ""},
		{[]string{"a"}, ", ", " and ", "a"},
		{[]string{"a", "b"}, ", ", " and ", "a and b"},
		{[]string{"a", "b", "c"}, ", ", " and ", "a, b and c"},
		{[]string{"a", "b", "c"}, ", ", "", "a, b, c"},
	}
	for _, c := range cases {
		if got := Join(c.in, c.sep, c.finalSep); got != c.want {
			t.Errorf("Join(%v,%q,%q): got %q want %q", c.in, c.sep, c.finalSep, got, c.want)
		}
	}
}

func TestCollapse(t *testing.T) {
	got := Collapse([][]int{{1, 2}, {3}, {4, 5, 6}})
	want := []int{1, 2, 3, 4, 5, 6}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
}

func TestCrossJoin(t *testing.T) {
	got := CrossJoin([]string{"a", "b"}, []string{"1", "2"})
	want := [][]string{{"a", "1"}, {"a", "2"}, {"b", "1"}, {"b", "2"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
}

// =========================================================================
// Map helpers
// =========================================================================

func TestGetSet(t *testing.T) {
	m := map[string]any{}
	Set(m, "user.name", "Tunde")
	Set(m, "user.email", "t@x.io")
	Set(m, "config.retries", 3)

	if got := Get(m, "user.name", "?"); got != "Tunde" {
		t.Errorf("got %v", got)
	}
	if got := Get(m, "config.retries", 0); got != 3 {
		t.Errorf("got %v", got)
	}
	if got := Get(m, "missing.key", "default"); got != "default" {
		t.Errorf("got %v", got)
	}
}

func TestGetThroughSlice(t *testing.T) {
	m := map[string]any{
		"users": []any{
			map[string]any{"name": "a"},
			map[string]any{"name": "b"},
		},
	}
	if got := Get(m, "users.0.name", ""); got != "a" {
		t.Errorf("got %v", got)
	}
	if got := Get(m, "users.1.name", ""); got != "b" {
		t.Errorf("got %v", got)
	}
	if got := Get(m, "users.99.name", "fallback"); got != "fallback" {
		t.Errorf("got %v", got)
	}
}

func TestHasAndHasAny(t *testing.T) {
	m := map[string]any{"a": map[string]any{"b": 1}, "c": 2}

	if !Has(m, "a.b", "c") {
		t.Error("HasAll true case")
	}
	if Has(m, "a.b", "missing") {
		t.Error("HasAll false case")
	}
	if !HasAny(m, "missing", "c") {
		t.Error("HasAny true case")
	}
	if HasAny(m, "x", "y") {
		t.Error("HasAny false case")
	}
}

func TestForget(t *testing.T) {
	m := map[string]any{"a": map[string]any{"b": 1, "c": 2}}
	Forget(m, "a.b")
	if Has(m, "a.b") {
		t.Error("a.b should be gone")
	}
	if !Has(m, "a.c") {
		t.Error("a.c should still exist")
	}
}

func TestPull(t *testing.T) {
	m := map[string]any{"a": 1, "b": 2}
	v, ok := Pull(m, "a", -1)
	if !ok || v != 1 {
		t.Errorf("Pull: %v %v", v, ok)
	}
	if _, exists := m["a"]; exists {
		t.Error("a should be removed")
	}

	v, ok = Pull(m, "missing", -1)
	if ok || v != -1 {
		t.Errorf("missing pull: %v %v", v, ok)
	}
}

func TestAdd(t *testing.T) {
	m := map[string]any{"a": 1}
	Add(m, "a", 99) // should NOT override
	Add(m, "b", 2)  // should set
	if m["a"] != 1 {
		t.Errorf("Add overrode existing: %v", m["a"])
	}
	if m["b"] != 2 {
		t.Errorf("Add did not set new: %v", m["b"])
	}
}

func TestOnlyAndExcept(t *testing.T) {
	m := map[string]any{"a": 1, "b": 2, "c": 3}
	o := Only(m, "a", "c")
	if !reflect.DeepEqual(o, map[string]any{"a": 1, "c": 3}) {
		t.Errorf("Only: %v", o)
	}
	e := Except(m, "a", "c")
	if !reflect.DeepEqual(e, map[string]any{"b": 2}) {
		t.Errorf("Except: %v", e)
	}
}

func TestDotAndUndot(t *testing.T) {
	nested := map[string]any{
		"user": map[string]any{
			"name":  "Tunde",
			"email": "t@x.io",
		},
		"flag": true,
	}

	flat := Dot(nested)
	if flat["user.name"] != "Tunde" || flat["user.email"] != "t@x.io" || flat["flag"] != true {
		t.Errorf("Dot: %v", flat)
	}

	roundtrip := Undot(flat)
	innerUser, ok := roundtrip["user"].(map[string]any)
	if !ok || innerUser["name"] != "Tunde" {
		t.Errorf("Undot: %v", roundtrip)
	}
}

func TestFlatten(t *testing.T) {
	nested := []any{
		1,
		[]any{2, 3, []any{4, 5}},
		6,
	}
	got := Flatten(nested, -1)
	if len(got) != 6 {
		t.Errorf("flatten unlimited: %v", got)
	}

	got = Flatten(nested, 1)
	// 1 level: nested inner should remain a slice
	if len(got) != 5 {
		t.Errorf("flatten depth 1: got %v len=%d", got, len(got))
	}
}

func TestQuery(t *testing.T) {
	m := map[string]any{
		"name": "Tunde",
		"page": 2,
	}
	got := Query(m)
	// Query has non-deterministic order — assert components.
	if !strings.Contains(got, "name=Tunde") {
		t.Errorf("missing name: %s", got)
	}
	if !strings.Contains(got, "page=2") {
		t.Errorf("missing page: %s", got)
	}
}

func TestIsAssocAndIsList(t *testing.T) {
	if !IsAssoc(map[string]int{"a": 1}) {
		t.Error("IsAssoc map")
	}
	if IsAssoc([]int{1, 2}) {
		t.Error("IsAssoc list false")
	}
	if !IsList([]int{1, 2}) {
		t.Error("IsList slice")
	}
	if IsList(map[string]int{}) {
		t.Error("IsList map false")
	}
}

func TestToCssClasses(t *testing.T) {
	got := ToCssClasses(map[string]bool{
		"btn":         true,
		"btn-primary": true,
		"is-loading":  false,
	})
	// Sorted output: "btn btn-primary"
	if got != "btn btn-primary" {
		t.Errorf("got %q", got)
	}
}

func TestToCssStyles(t *testing.T) {
	got := ToCssStyles(map[string]string{
		"color":      "red",
		"font-size":  "14px",
		"margin-top": "1em",
	})
	want := "color: red; font-size: 14px; margin-top: 1em;"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestPrependKeysWith(t *testing.T) {
	m := map[string]any{"a": 1, "b": 2}
	got := PrependKeysWith(m, "ns.")
	if got["ns.a"] != 1 || got["ns.b"] != 2 {
		t.Errorf("got %v", got)
	}
	if _, exists := m["ns.a"]; exists {
		t.Error("PrependKeysWith should not modify input")
	}
}

func TestDivide(t *testing.T) {
	keys, values := Divide(map[string]any{"a": 1, "b": 2})
	if len(keys) != 2 || len(values) != 2 {
		t.Errorf("got keys=%v values=%v", keys, values)
	}
}

func TestExists(t *testing.T) {
	m := map[string]any{"a": nil}
	if !Exists(m, "a") {
		t.Error("Exists should be true even for nil value")
	}
	if Exists(m, "b") {
		t.Error("Exists false case")
	}
}
