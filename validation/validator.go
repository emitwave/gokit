// Package validation provides declarative validation for Go.
//
// There are two entry points depending on whether your input is structured
// or freeform:
//
//	// Map-based (e.g. raw JSON decoded into map[string]any):
//	errs := validation.Make(data, validation.Rules{
//	    "email":    "required|email",
//	    "password": "required|string|min:8|confirmed",
//	    "age":      "required|integer|between:18,120",
//	})
//	if errs.Any() { /* return 422 with errs */ }
//
//	// Struct-based (using validate: tags from go-playground/validator):
//	errs := validation.ValidateStruct(req)
//
// Both produce the same *Errors bag, so your HTTP layer can render them
// with one helper (see the response package).
package validation

import (
	"fmt"
	"sync"
)

// Rules is a rule map: field → pipe-separated rule string.
// Using a named type makes call sites read better than raw map literals.
type Rules map[string]string

// MessageOverrides lets callers override default messages per rule
// globally (key: "rule_name") or per field+rule (key: "field.rule_name").
//
//	validation.MessageOverrides{
//	    "required":         "Please fill in :attribute.",
//	    "email.email":      "That email looks off — double-check it.",
//	}
type MessageOverrides map[string]string

// Validator is reusable and safe for concurrent use after configuration.
// Use New() when you want to register custom rules or override messages
// across many calls; use Make() for one-shot validation with defaults.
type Validator struct {
	mu       sync.RWMutex
	rules    map[string]RuleFn
	messages MessageOverrides
}

// New returns a Validator pre-loaded with all built-in rules. Customize it
// with Register() and SetMessages() before reusing across requests.
func New() *Validator {
	v := &Validator{
		rules:    make(map[string]RuleFn, len(builtinRules)),
		messages: make(MessageOverrides),
	}
	for name, fn := range builtinRules {
		v.rules[name] = fn
	}
	return v
}

// Register adds (or overrides) a rule. Safe to call after New() but before
// the validator is shared across goroutines.
func (v *Validator) Register(name string, fn RuleFn) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.rules[name] = fn
}

// RegisterWithMessage adds a rule and its default message in one call.
func (v *Validator) RegisterWithMessage(name, message string, fn RuleFn) {
	v.Register(name, fn)
	v.mu.Lock()
	v.messages[name] = message
	v.mu.Unlock()
}

// SetMessages merges overrides into the validator's message table.
// Field-specific overrides take precedence over rule-only overrides,
// which take precedence over built-in defaults.
func (v *Validator) SetMessages(m MessageOverrides) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for k, val := range m {
		v.messages[k] = val
	}
}

// Validate runs the rules against data and returns an error bag.
// The bag is always non-nil; check Any() to see if it has contents.
//
// Behavior notes:
//   - "nullable" short-circuits: if the value is nil/empty and nullable
//     is in the rule list, no other rules run for that field
//   - "required" failures short-circuit: subsequent rules don't run for
//     a missing required field — one error per field is enough
//   - Unknown rules produce a clear error message naming the rule, so
//     typos surface during development instead of silently passing
func (v *Validator) Validate(data map[string]any, rules Rules) *Errors {
	errs := NewErrors()
	if data == nil {
		data = map[string]any{}
	}

	// Snapshot rule + message tables under read lock so concurrent calls
	// to Register/SetMessages don't tear partial state into our loop.
	v.mu.RLock()
	ruleSet := v.rules
	msgSet := v.messages
	v.mu.RUnlock()

	for field, spec := range rules {
		parsed := parseRules(spec)
		value, present := data[field]

		nullable := hasRule(parsed, "nullable")
		isEmpty := !present || value == nil || value == ""

		// Skip the rest of the rules entirely for empty + nullable fields.
		if nullable && isEmpty {
			continue
		}

		// If "required" fails, don't run subsequent rules — they'd just
		// pile redundant errors onto an already-known-missing field.
		requiredFailed := false

		for _, r := range parsed {
			if r.Name == "nullable" {
				continue
			}

			fn, ok := ruleSet[r.Name]
			if !ok {
				errs.Add(field, fmt.Sprintf("unknown validation rule %q", r.Name))
				continue
			}

			// For non-required rules on empty values: skip silently. The
			// convention here is to opt into presence with "required";
			// otherwise empty values are considered valid for type/format
			// checks. (Required itself still runs and will fail.)
			if r.Name != "required" && isEmpty && !requiredFailed {
				continue
			}

			passed, params := fn(field, value, r.Params, data)
			if !passed {
				if r.Name == "required" {
					requiredFailed = true
				}
				errs.Add(field, v.formatError(field, r.Name, params, msgSet))
				// One error per rule per field is enough; move to next rule.
			}
		}
	}

	return errs
}

// formatError picks the right message template for a (field, rule) pair
// and substitutes placeholders. Lookup order: field.rule → rule → default.
func (v *Validator) formatError(field, ruleName string, ruleParams map[string]string, overrides MessageOverrides) string {
	if tpl, ok := overrides[field+"."+ruleName]; ok {
		return formatMessage(tpl, field, ruleParams)
	}
	if tpl, ok := overrides[ruleName]; ok {
		return formatMessage(tpl, field, ruleParams)
	}
	if tpl, ok := defaultMessages[ruleName]; ok {
		return formatMessage(tpl, field, ruleParams)
	}
	return formatMessage(defaultMessages["_default"], field, ruleParams)
}

// ---------- package-level convenience -----------------------------------

// defaultValidator is a process-wide instance used by Make(). Custom rules
// registered on it are visible to all Make() calls. For request-scoped
// customization, use New() instead.
var defaultValidator = New()

// Make validates data against rules using the package-level validator.
// This is the one-liner most callers want:
//
//	if errs := validation.Make(input, rules); errs.Any() { ... }
func Make(data map[string]any, rules Rules) *Errors {
	return defaultValidator.Validate(data, rules)
}

// MakeWith validates using a caller-provided validator (handy for tests
// or per-tenant rule sets).
func MakeWith(v *Validator, data map[string]any, rules Rules) *Errors {
	return v.Validate(data, rules)
}

// Register adds a custom rule to the package-level validator.
func Register(name string, fn RuleFn) {
	defaultValidator.Register(name, fn)
}

// RegisterWithMessage adds a custom rule and its default message to the
// package-level validator.
func RegisterWithMessage(name, message string, fn RuleFn) {
	defaultValidator.RegisterWithMessage(name, message, fn)
}

// SetMessages merges message overrides into the package-level validator.
func SetMessages(m MessageOverrides) {
	defaultValidator.SetMessages(m)
}
