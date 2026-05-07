package validation

import (
	"strings"
)

// defaultMessages are the built-in messages for each rule. They support
// :attribute, :value, :min, :max, :other, :values placeholders that get
// replaced at format time.
var defaultMessages = map[string]string{
	"required":     "The :attribute field is required.",
	"string":       "The :attribute must be a string.",
	"numeric":      "The :attribute must be a number.",
	"integer":      "The :attribute must be an integer.",
	"boolean":      "The :attribute field must be true or false.",
	"email":        "The :attribute must be a valid email address.",
	"url":          "The :attribute must be a valid URL.",
	"uuid":         "The :attribute must be a valid UUID.",
	"alpha":        "The :attribute may only contain letters.",
	"alpha_num":    "The :attribute may only contain letters and numbers.",
	"alpha_dash":   "The :attribute may only contain letters, numbers, dashes and underscores.",
	"min":          "The :attribute must be at least :min.",
	"max":          "The :attribute may not be greater than :max.",
	"between":      "The :attribute must be between :min and :max.",
	"in":           "The selected :attribute is invalid.",
	"not_in":       "The selected :attribute is invalid.",
	"regex":        "The :attribute format is invalid.",
	"confirmed":    "The :attribute confirmation does not match.",
	"same":         "The :attribute and :other must match.",
	"different":    "The :attribute and :other must be different.",
	"date":         "The :attribute is not a valid date.",
	"before":       "The :attribute must be a date before :value.",
	"after":        "The :attribute must be a date after :value.",
	"json":         "The :attribute must be a valid JSON string.",
	"ip":           "The :attribute must be a valid IP address.",
	"starts_with":  "The :attribute must start with one of the following: :values.",
	"ends_with":    "The :attribute must end with one of the following: :values.",
	"_default":     "The :attribute field is invalid.",
}

// formatMessage substitutes placeholders in a message template.
// Unknown placeholders are left intact so they show up obviously in tests.
func formatMessage(template, field string, params map[string]string) string {
	msg := strings.ReplaceAll(template, ":attribute", humanize(field))
	for k, v := range params {
		msg = strings.ReplaceAll(msg, ":"+k, v)
	}
	return msg
}

// humanize turns "first_name" / "firstName" into "first name" so default
// messages read naturally without forcing users to register every label.
func humanize(field string) string {
	// snake_case → spaces
	out := strings.ReplaceAll(field, "_", " ")
	// camelCase → spaces (insert space before each uppercase letter that
	// follows a lowercase letter)
	var b strings.Builder
	for i, r := range out {
		if i > 0 && r >= 'A' && r <= 'Z' {
			prev := rune(out[i-1])
			if prev >= 'a' && prev <= 'z' {
				b.WriteByte(' ')
			}
		}
		b.WriteRune(r)
	}
	return strings.ToLower(b.String())
}
