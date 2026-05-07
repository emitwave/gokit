package validation

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseRules(t *testing.T) {
	tests := []struct {
		spec string
		want []parsedRule
	}{
		{"required|email", []parsedRule{
			{"required", []string{}},
			{"email", []string{}},
		}},
		{"max:255", []parsedRule{
			{"max", []string{"255"}},
		}},
		{"in:admin,user,guest", []parsedRule{
			{"in", []string{"admin", "user", "guest"}},
		}},
		{"required|between:1,10", []parsedRule{
			{"required", []string{}},
			{"between", []string{"1", "10"}},
		}},
		{"", nil},
		{"required||email", []parsedRule{
			{"required", []string{}},
			{"email", []string{}},
		}},
	}
	for _, tt := range tests {
		got := parseRules(tt.spec)
		if !equalParsed(got, tt.want) {
			t.Errorf("parseRules(%q) = %#v, want %#v", tt.spec, got, tt.want)
		}
	}
}

func equalParsed(a, b []parsedRule) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name {
			return false
		}
		if len(a[i].Params) != len(b[i].Params) {
			return false
		}
		for j := range a[i].Params {
			if a[i].Params[j] != b[i].Params[j] {
				return false
			}
		}
	}
	return true
}

func TestRequired(t *testing.T) {
	rules := Rules{"name": "required"}

	if errs := Make(map[string]any{"name": "Tunde"}, rules); errs.Any() {
		t.Errorf("expected no errors, got %v", errs.All())
	}
	if errs := Make(map[string]any{}, rules); !errs.Has("name") {
		t.Errorf("expected required error, got %v", errs.All())
	}
	if errs := Make(map[string]any{"name": ""}, rules); !errs.Has("name") {
		t.Errorf("expected required error for empty string")
	}
	if errs := Make(map[string]any{"name": "   "}, rules); !errs.Has("name") {
		t.Errorf("expected required error for whitespace-only string")
	}
}

func TestEmail(t *testing.T) {
	rules := Rules{"email": "required|email"}

	cases := map[string]bool{ // email → shouldFail
		"user@example.com":      false,
		"first.last@host.co.uk": false,
		"not-an-email":          true,
		"missing@":              true,
		"":                      true, // required catches it
	}
	for email, shouldFail := range cases {
		errs := Make(map[string]any{"email": email}, rules)
		if shouldFail && !errs.Has("email") {
			t.Errorf("email=%q should fail, didn't", email)
		}
		if !shouldFail && errs.Has("email") {
			t.Errorf("email=%q should pass, got %v", email, errs.Get("email"))
		}
	}
}

func TestMinMax(t *testing.T) {
	// String length
	errs := Make(map[string]any{"name": "ab"}, Rules{"name": "min:3"})
	if !errs.Has("name") {
		t.Errorf("expected min failure for 'ab' against min:3")
	}
	errs = Make(map[string]any{"name": "abcd"}, Rules{"name": "min:3"})
	if errs.Any() {
		t.Errorf("expected no errors for 'abcd' against min:3, got %v", errs.All())
	}

	// Numeric
	errs = Make(map[string]any{"age": 17}, Rules{"age": "min:18"})
	if !errs.Has("age") {
		t.Errorf("expected min failure for age=17 against min:18")
	}

	// Max
	errs = Make(map[string]any{"name": "abcdef"}, Rules{"name": "max:5"})
	if !errs.Has("name") {
		t.Errorf("expected max failure for 'abcdef' against max:5")
	}
}

func TestBetween(t *testing.T) {
	errs := Make(map[string]any{"score": 75}, Rules{"score": "between:0,100"})
	if errs.Any() {
		t.Errorf("expected pass, got %v", errs.All())
	}
	errs = Make(map[string]any{"score": 150}, Rules{"score": "between:0,100"})
	if !errs.Has("score") {
		t.Errorf("expected between failure for 150")
	}
}

func TestIn(t *testing.T) {
	rules := Rules{"role": "required|in:admin,user,guest"}
	if errs := Make(map[string]any{"role": "admin"}, rules); errs.Any() {
		t.Errorf("expected pass for admin, got %v", errs.All())
	}
	if errs := Make(map[string]any{"role": "superuser"}, rules); !errs.Has("role") {
		t.Errorf("expected failure for superuser")
	}
}

func TestConfirmed(t *testing.T) {
	rules := Rules{"password": "required|min:8|confirmed"}
	data := map[string]any{
		"password":              "secret123",
		"password_confirmation": "secret123",
	}
	if errs := Make(data, rules); errs.Any() {
		t.Errorf("expected pass, got %v", errs.All())
	}

	data["password_confirmation"] = "different"
	if errs := Make(data, rules); !errs.Has("password") {
		t.Errorf("expected confirmed failure")
	}
}

func TestNullable(t *testing.T) {
	rules := Rules{"bio": "nullable|string|max:500"}

	// Missing → passes (nullable)
	if errs := Make(map[string]any{}, rules); errs.Any() {
		t.Errorf("expected pass for missing nullable field, got %v", errs.All())
	}
	// Present but empty → passes (nullable)
	if errs := Make(map[string]any{"bio": ""}, rules); errs.Any() {
		t.Errorf("expected pass for empty nullable field, got %v", errs.All())
	}
	// Present with value → other rules apply
	long := strings.Repeat("a", 600)
	if errs := Make(map[string]any{"bio": long}, rules); !errs.Has("bio") {
		t.Errorf("expected max failure on long nullable value")
	}
}

func TestUnknownRule(t *testing.T) {
	errs := Make(map[string]any{"x": "y"}, Rules{"x": "potato"})
	if !errs.Has("x") {
		t.Fatalf("expected error for unknown rule")
	}
	if !strings.Contains(errs.First("x"), "potato") {
		t.Errorf("error should mention the unknown rule name, got %q", errs.First("x"))
	}
}

func TestCustomRule(t *testing.T) {
	v := New()
	v.RegisterWithMessage(
		"even",
		"The :attribute must be even.",
		func(field string, value any, params []string, data map[string]any) (bool, map[string]string) {
			n, ok := value.(int)
			if !ok {
				return false, nil
			}
			return n%2 == 0, nil
		},
	)

	errs := v.Validate(map[string]any{"n": 3}, Rules{"n": "even"})
	if !errs.Has("n") {
		t.Errorf("expected even failure for 3")
	}
	if !strings.Contains(errs.First("n"), "even") {
		t.Errorf("expected message to mention 'even', got %q", errs.First("n"))
	}

	errs = v.Validate(map[string]any{"n": 4}, Rules{"n": "even"})
	if errs.Any() {
		t.Errorf("expected pass for 4")
	}
}

func TestStructValidation(t *testing.T) {
	type Req struct {
		Name  string `json:"name" validate:"required,max=10"`
		Email string `json:"email" validate:"required,email"`
		Age   int    `json:"age" validate:"required,gte=18"`
	}

	errs := ValidateStruct(Req{Name: "", Email: "nope", Age: 10})
	if !errs.Has("name") || !errs.Has("email") || !errs.Has("age") {
		t.Errorf("expected errors on all three fields, got %v", errs.All())
	}

	// Field keys should use json tag names, not Go field names.
	if errs.Has("Name") {
		t.Errorf("expected json field name 'name', not 'Name'")
	}
}

func TestURLRule(t *testing.T) {
	rules := Rules{"site": "url"}
	cases := map[string]bool{
		"https://emitwave.com":    false,
		"http://localhost:3000/x": false,
		"not a url":               true,
		"emitwave.com":            true, // missing scheme
	}
	for v, shouldFail := range cases {
		errs := Make(map[string]any{"site": v}, rules)
		if shouldFail && !errs.Has("site") {
			t.Errorf("url=%q should fail", v)
		}
		if !shouldFail && errs.Any() {
			t.Errorf("url=%q should pass, got %v", v, errs.All())
		}
	}
}

func TestUUIDRule(t *testing.T) {
	rules := Rules{"id": "uuid"}
	if errs := Make(map[string]any{"id": "550e8400-e29b-41d4-a716-446655440000"}, rules); errs.Any() {
		t.Errorf("expected pass for valid UUID, got %v", errs.All())
	}
	if errs := Make(map[string]any{"id": "not-a-uuid"}, rules); !errs.Has("id") {
		t.Errorf("expected uuid failure")
	}
}

func TestRegexRule(t *testing.T) {
	// Pattern matches lowercase alpha only.
	rules := Rules{"slug": "regex:^[a-z]+$"}
	if errs := Make(map[string]any{"slug": "hello"}, rules); errs.Any() {
		t.Errorf("expected pass, got %v", errs.All())
	}
	if errs := Make(map[string]any{"slug": "Hello"}, rules); !errs.Has("slug") {
		t.Errorf("expected regex failure for capitalized input")
	}
}

func TestDateRules(t *testing.T) {
	if errs := Make(map[string]any{"d": "2025-01-15"}, Rules{"d": "date"}); errs.Any() {
		t.Errorf("expected pass, got %v", errs.All())
	}
	if errs := Make(map[string]any{"d": "not-a-date"}, Rules{"d": "date"}); !errs.Has("d") {
		t.Errorf("expected date failure")
	}
	// before / after composition
	rules := Rules{"d": "date|after:2024-01-01|before:2026-01-01"}
	if errs := Make(map[string]any{"d": "2025-06-15"}, rules); errs.Any() {
		t.Errorf("expected pass for date in range, got %v", errs.All())
	}
	if errs := Make(map[string]any{"d": "2023-01-01"}, rules); !errs.Has("d") {
		t.Errorf("expected after failure for too-early date")
	}
}

func TestMessageOverrides(t *testing.T) {
	v := New()
	v.SetMessages(MessageOverrides{
		"required":    "Please provide :attribute.",
		"email.email": "That email is bogus.",
	})

	errs := v.Validate(map[string]any{}, Rules{"first_name": "required"})
	if got := errs.First("first_name"); !strings.Contains(got, "Please provide first name") {
		t.Errorf("rule-level override + humanize failed, got %q", got)
	}

	errs = v.Validate(map[string]any{"email": "nope"}, Rules{"email": "email"})
	if got := errs.First("email"); got != "That email is bogus." {
		t.Errorf("field-specific override failed, got %q", got)
	}
}

func TestErrorsJSON(t *testing.T) {
	errs := NewErrors()
	errs.Add("email", "The email field is required.")
	errs.Add("email", "The email must be a valid email address.")
	errs.Add("age", "The age must be at least 18.")

	b, err := json.Marshal(errs)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string][]string
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got["email"]) != 2 || len(got["age"]) != 1 {
		t.Errorf("unexpected JSON shape: %s", b)
	}
}
