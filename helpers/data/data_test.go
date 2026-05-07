package data

import (
	"reflect"
	"sort"
	"testing"
)

func sample() map[string]any {
	return map[string]any{
		"products": []any{
			map[string]any{"name": "shirt", "price": 25.0},
			map[string]any{"name": "pants", "price": 40.0},
			map[string]any{"name": "shoes", "price": 80.0},
		},
		"user": map[string]any{
			"name": "Tunde",
			"profile": map[string]any{
				"email": "t@x.io",
			},
		},
	}
}

func TestGetSimplePath(t *testing.T) {
	target := sample()
	if got := Get(target, "user.name", ""); got != "Tunde" {
		t.Errorf("got %v", got)
	}
	if got := Get(target, "user.profile.email", ""); got != "t@x.io" {
		t.Errorf("got %v", got)
	}
}

func TestGetSliceIndex(t *testing.T) {
	target := sample()
	if got := Get(target, "products.0.name", ""); got != "shirt" {
		t.Errorf("got %v", got)
	}
	if got := Get(target, "products.2.price", 0.0); got != 80.0 {
		t.Errorf("got %v", got)
	}
}

func TestGetWildcard(t *testing.T) {
	target := sample()
	got := Get(target, "products.*.name", []any{}).([]any)
	want := []any{"shirt", "pants", "shoes"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestGetWildcardOnMap(t *testing.T) {
	// Wildcard on a map returns its values.
	target := map[string]any{
		"users": map[string]any{
			"a": map[string]any{"name": "Anna"},
			"b": map[string]any{"name": "Ben"},
		},
	}
	got := Get(target, "users.*.name", []any{}).([]any)
	// Order on a map is non-deterministic — sort to compare.
	gotStrs := make([]string, len(got))
	for i, v := range got {
		gotStrs[i] = v.(string)
	}
	sort.Strings(gotStrs)
	if !reflect.DeepEqual(gotStrs, []string{"Anna", "Ben"}) {
		t.Errorf("got %v", gotStrs)
	}
}

func TestGetMissingReturnsDefault(t *testing.T) {
	target := sample()
	if got := Get(target, "user.profile.phone", "n/a"); got != "n/a" {
		t.Errorf("got %v", got)
	}
	if got := Get(target, "missing.path", 42); got != 42 {
		t.Errorf("got %v", got)
	}
}

func TestSet(t *testing.T) {
	target := sample()
	Set(target, "user.profile.phone", "555-1234")
	if got := Get(target, "user.profile.phone", ""); got != "555-1234" {
		t.Errorf("Set didn't take: %v", got)
	}
	// Brand new path with intermediate map creation
	Set(target, "config.notifications.email", true)
	if got := Get(target, "config.notifications.email", false); got != true {
		t.Errorf("Set with new path: %v", got)
	}
}

func TestFill(t *testing.T) {
	target := sample()
	// Fill on existing path should not overwrite
	Fill(target, "user.name", "OVERRIDE")
	if got := Get(target, "user.name", ""); got != "Tunde" {
		t.Errorf("Fill overrode: %v", got)
	}
	// Fill on missing path sets it
	Fill(target, "user.profile.phone", "555")
	if got := Get(target, "user.profile.phone", ""); got != "555" {
		t.Errorf("Fill didn't set: %v", got)
	}
}

func TestForget(t *testing.T) {
	target := sample()
	if !Forget(target, "user.profile.email") {
		t.Error("Forget should report success")
	}
	if got := Get(target, "user.profile.email", "missing"); got != "missing" {
		t.Errorf("Forget didn't remove: %v", got)
	}
	// Forgetting a non-existent path returns false.
	if Forget(target, "no.such.path") {
		t.Error("Forget on missing path should be false")
	}
}

func TestHeadAndLast(t *testing.T) {
	s := []int{1, 2, 3, 4}
	if got := Head(s, -1); got != 1 {
		t.Errorf("Head: %d", got)
	}
	if got := Last(s, -1); got != 4 {
		t.Errorf("Last: %d", got)
	}
	if got := Head([]int{}, -1); got != -1 {
		t.Errorf("Head empty: %d", got)
	}
	if got := Last([]int{}, -1); got != -1 {
		t.Errorf("Last empty: %d", got)
	}
}

func TestGetEmptyPathReturnsTarget(t *testing.T) {
	target := sample()
	got := Get(target, "", nil)
	if !reflect.DeepEqual(got, target) {
		t.Error("empty path should return the target itself")
	}
}
