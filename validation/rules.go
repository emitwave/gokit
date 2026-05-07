package validation

import "strings"

// parsedRule is a single rule pulled from a pipe-separated rule string.
// Example: "max:255" → {Name:"max", Params:["255"]}
//          "in:admin,user,guest" → {Name:"in", Params:["admin","user","guest"]}
type parsedRule struct {
	Name   string
	Params []string
}

// parseRules splits a pipe-separated rule string into individual rules.
// We split on '|' first, then on ':' to separate the rule name from its
// parameter list, then on ',' to split parameters.
//
// Edge cases handled:
//   - Empty segments are skipped: "required||email" → [required, email]
//   - Whitespace around rule names/params is trimmed
//   - Rules without params have empty Params slices (not nil) for ergonomic
//     iteration in callers
func parseRules(spec string) []parsedRule {
	if spec == "" {
		return nil
	}
	segments := strings.Split(spec, "|")
	out := make([]parsedRule, 0, len(segments))
	for _, seg := range segments {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		var name, paramStr string
		if idx := strings.Index(seg, ":"); idx >= 0 {
			name = strings.TrimSpace(seg[:idx])
			paramStr = seg[idx+1:]
		} else {
			name = seg
		}
		var params []string
		if paramStr != "" {
			raw := strings.Split(paramStr, ",")
			params = make([]string, 0, len(raw))
			for _, p := range raw {
				params = append(params, strings.TrimSpace(p))
			}
		} else {
			params = []string{}
		}
		out = append(out, parsedRule{Name: name, Params: params})
	}
	return out
}

// hasRule reports whether a parsed rule list contains a rule by name.
// Used internally to detect "nullable" before evaluating other rules.
func hasRule(rules []parsedRule, name string) bool {
	for _, r := range rules {
		if r.Name == name {
			return true
		}
	}
	return false
}
