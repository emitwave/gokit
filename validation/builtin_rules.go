package validation

import (
	"encoding/json"
	"net"
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// RuleFn is the signature every rule implements.
//
//   - field:  the field name being validated (e.g. "email")
//   - value:  the raw value from the input map (may be nil)
//   - params: parameters parsed from the rule (e.g. "min:8" → ["8"])
//   - data:   the entire input map, so cross-field rules (same/different/
//             confirmed) can look up siblings
//
// Returns (passes, placeholderParams). If passes is false, the validator
// looks up the message template and merges placeholderParams in.
type RuleFn func(field string, value any, params []string, data map[string]any) (bool, map[string]string)

// builtinRules is the global registry of rule name → implementation.
// Custom rules added via Register are merged here at validator-creation time.
var builtinRules = map[string]RuleFn{
	"required":    ruleRequired,
	"string":      ruleString,
	"numeric":     ruleNumeric,
	"integer":     ruleInteger,
	"boolean":     ruleBoolean,
	"email":       ruleEmail,
	"url":         ruleURL,
	"uuid":        ruleUUID,
	"alpha":       ruleAlpha,
	"alpha_num":   ruleAlphaNum,
	"alpha_dash":  ruleAlphaDash,
	"min":         ruleMin,
	"max":         ruleMax,
	"between":     ruleBetween,
	"in":          ruleIn,
	"not_in":      ruleNotIn,
	"regex":       ruleRegex,
	"confirmed":   ruleConfirmed,
	"same":        ruleSame,
	"different":   ruleDifferent,
	"date":        ruleDate,
	"before":      ruleBefore,
	"after":       ruleAfter,
	"json":        ruleJSON,
	"ip":          ruleIP,
	"starts_with": ruleStartsWith,
	"ends_with":   ruleEndsWith,
}

// ---------- presence -----------------------------------------------------

func ruleRequired(_ string, value any, _ []string, _ map[string]any) (bool, map[string]string) {
	if value == nil {
		return false, nil
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v) != "", nil
	case []any:
		return len(v) > 0, nil
	case map[string]any:
		return len(v) > 0, nil
	}
	return true, nil
}

// ---------- type checks --------------------------------------------------

func ruleString(_ string, value any, _ []string, _ map[string]any) (bool, map[string]string) {
	if value == nil {
		return true, nil // nullable handling is upstream
	}
	_, ok := value.(string)
	return ok, nil
}

func ruleNumeric(_ string, value any, _ []string, _ map[string]any) (bool, map[string]string) {
	if value == nil {
		return true, nil
	}
	switch v := value.(type) {
	case int, int32, int64, float32, float64, json.Number:
		return true, nil
	case string:
		_, err := strconv.ParseFloat(v, 64)
		return err == nil, nil
	}
	return false, nil
}

func ruleInteger(_ string, value any, _ []string, _ map[string]any) (bool, map[string]string) {
	if value == nil {
		return true, nil
	}
	switch v := value.(type) {
	case int, int32, int64:
		return true, nil
	case float32:
		return float32(int64(v)) == v, nil
	case float64:
		// JSON numbers always decode as float64; treat whole floats as ints
		return float64(int64(v)) == v, nil
	case json.Number:
		_, err := v.Int64()
		return err == nil, nil
	case string:
		_, err := strconv.ParseInt(v, 10, 64)
		return err == nil, nil
	}
	return false, nil
}

func ruleBoolean(_ string, value any, _ []string, _ map[string]any) (bool, map[string]string) {
	if value == nil {
		return true, nil
	}
	switch v := value.(type) {
	case bool:
		return true, nil
	case string:
		switch strings.ToLower(v) {
		case "true", "false", "1", "0":
			return true, nil
		}
	case int, int32, int64:
		n := toInt64(v)
		return n == 0 || n == 1, nil
	}
	return false, nil
}

// ---------- format checks ------------------------------------------------

func ruleEmail(_ string, value any, _ []string, _ map[string]any) (bool, map[string]string) {
	s, ok := value.(string)
	if !ok || s == "" {
		return value == nil, nil
	}
	_, err := mail.ParseAddress(s)
	return err == nil, nil
}

func ruleURL(_ string, value any, _ []string, _ map[string]any) (bool, map[string]string) {
	s, ok := value.(string)
	if !ok || s == "" {
		return value == nil, nil
	}
	u, err := url.Parse(s)
	return err == nil && u.Scheme != "" && u.Host != "", nil
}

func ruleUUID(_ string, value any, _ []string, _ map[string]any) (bool, map[string]string) {
	s, ok := value.(string)
	if !ok || s == "" {
		return value == nil, nil
	}
	_, err := uuid.Parse(s)
	return err == nil, nil
}

var (
	reAlpha     = regexp.MustCompile(`^[A-Za-z]+$`)
	reAlphaNum  = regexp.MustCompile(`^[A-Za-z0-9]+$`)
	reAlphaDash = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

func ruleAlpha(_ string, value any, _ []string, _ map[string]any) (bool, map[string]string) {
	s, ok := value.(string)
	if !ok {
		return value == nil, nil
	}
	return reAlpha.MatchString(s), nil
}

func ruleAlphaNum(_ string, value any, _ []string, _ map[string]any) (bool, map[string]string) {
	s, ok := value.(string)
	if !ok {
		return value == nil, nil
	}
	return reAlphaNum.MatchString(s), nil
}

func ruleAlphaDash(_ string, value any, _ []string, _ map[string]any) (bool, map[string]string) {
	s, ok := value.(string)
	if !ok {
		return value == nil, nil
	}
	return reAlphaDash.MatchString(s), nil
}

func ruleJSON(_ string, value any, _ []string, _ map[string]any) (bool, map[string]string) {
	s, ok := value.(string)
	if !ok {
		return value == nil, nil
	}
	var v any
	return json.Unmarshal([]byte(s), &v) == nil, nil
}

func ruleIP(_ string, value any, _ []string, _ map[string]any) (bool, map[string]string) {
	s, ok := value.(string)
	if !ok {
		return value == nil, nil
	}
	return net.ParseIP(s) != nil, nil
}

// ---------- size / range -------------------------------------------------

// sizeOf returns the comparison size for value: length for strings/slices/
// maps, numeric value for numbers. The second return is false when the
// value can't be sized (e.g. unsupported type).
func sizeOf(value any) (float64, bool) {
	switch v := value.(type) {
	case string:
		return float64(len([]rune(v))), true
	case []any:
		return float64(len(v)), true
	case map[string]any:
		return float64(len(v)), true
	case int:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case float32:
		return float64(v), true
	case float64:
		return v, true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	}
	return 0, false
}

func ruleMin(_ string, value any, params []string, _ map[string]any) (bool, map[string]string) {
	if len(params) == 0 || value == nil {
		return value == nil, nil
	}
	min, err := strconv.ParseFloat(params[0], 64)
	if err != nil {
		return false, nil
	}
	size, ok := sizeOf(value)
	if !ok {
		return false, nil
	}
	return size >= min, map[string]string{"min": params[0]}
}

func ruleMax(_ string, value any, params []string, _ map[string]any) (bool, map[string]string) {
	if len(params) == 0 || value == nil {
		return value == nil, nil
	}
	max, err := strconv.ParseFloat(params[0], 64)
	if err != nil {
		return false, nil
	}
	size, ok := sizeOf(value)
	if !ok {
		return false, nil
	}
	return size <= max, map[string]string{"max": params[0]}
}

func ruleBetween(_ string, value any, params []string, _ map[string]any) (bool, map[string]string) {
	if len(params) < 2 || value == nil {
		return value == nil, nil
	}
	min, err1 := strconv.ParseFloat(params[0], 64)
	max, err2 := strconv.ParseFloat(params[1], 64)
	if err1 != nil || err2 != nil {
		return false, nil
	}
	size, ok := sizeOf(value)
	if !ok {
		return false, nil
	}
	return size >= min && size <= max, map[string]string{"min": params[0], "max": params[1]}
}

// ---------- membership ---------------------------------------------------

func ruleIn(_ string, value any, params []string, _ map[string]any) (bool, map[string]string) {
	s := toString(value)
	for _, p := range params {
		if p == s {
			return true, nil
		}
	}
	return false, nil
}

func ruleNotIn(_ string, value any, params []string, _ map[string]any) (bool, map[string]string) {
	s := toString(value)
	for _, p := range params {
		if p == s {
			return false, nil
		}
	}
	return true, nil
}

func ruleStartsWith(_ string, value any, params []string, _ map[string]any) (bool, map[string]string) {
	s, ok := value.(string)
	if !ok {
		return value == nil, nil
	}
	for _, p := range params {
		if strings.HasPrefix(s, p) {
			return true, nil
		}
	}
	return false, map[string]string{"values": strings.Join(params, ", ")}
}

func ruleEndsWith(_ string, value any, params []string, _ map[string]any) (bool, map[string]string) {
	s, ok := value.(string)
	if !ok {
		return value == nil, nil
	}
	for _, p := range params {
		if strings.HasSuffix(s, p) {
			return true, nil
		}
	}
	return false, map[string]string{"values": strings.Join(params, ", ")}
}

// ---------- regex --------------------------------------------------------

func ruleRegex(_ string, value any, params []string, _ map[string]any) (bool, map[string]string) {
	if len(params) == 0 {
		return false, nil
	}
	s, ok := value.(string)
	if !ok {
		return value == nil, nil
	}
	// Rule strings can't contain pipes or commas safely, so we expect the
	// caller to pass the pattern via WithRegex or use struct tags for
	// complex patterns. Compile lazily per call — fine for our scale.
	re, err := regexp.Compile(params[0])
	if err != nil {
		return false, nil
	}
	return re.MatchString(s), nil
}

// ---------- cross-field --------------------------------------------------

func ruleConfirmed(field string, value any, _ []string, data map[string]any) (bool, map[string]string) {
	other, ok := data[field+"_confirmation"]
	if !ok {
		return false, nil
	}
	return toString(value) == toString(other), nil
}

func ruleSame(field string, value any, params []string, data map[string]any) (bool, map[string]string) {
	if len(params) == 0 {
		return false, nil
	}
	return toString(value) == toString(data[params[0]]), map[string]string{"other": humanize(params[0])}
}

func ruleDifferent(field string, value any, params []string, data map[string]any) (bool, map[string]string) {
	if len(params) == 0 {
		return false, nil
	}
	return toString(value) != toString(data[params[0]]), map[string]string{"other": humanize(params[0])}
}

// ---------- dates --------------------------------------------------------

// dateLayouts is the set of layouts we'll try when parsing date inputs.
// We accept ISO-8601 variants since that's what frontends actually send.
var dateLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

func parseDate(value any) (time.Time, bool) {
	s, ok := value.(string)
	if !ok {
		return time.Time{}, false
	}
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func ruleDate(_ string, value any, _ []string, _ map[string]any) (bool, map[string]string) {
	if value == nil {
		return true, nil
	}
	_, ok := parseDate(value)
	return ok, nil
}

func ruleBefore(_ string, value any, params []string, _ map[string]any) (bool, map[string]string) {
	if len(params) == 0 || value == nil {
		return value == nil, nil
	}
	v, ok1 := parseDate(value)
	ref, ok2 := parseDate(params[0])
	if !ok1 || !ok2 {
		return false, nil
	}
	return v.Before(ref), map[string]string{"value": params[0]}
}

func ruleAfter(_ string, value any, params []string, _ map[string]any) (bool, map[string]string) {
	if len(params) == 0 || value == nil {
		return value == nil, nil
	}
	v, ok1 := parseDate(value)
	ref, ok2 := parseDate(params[0])
	if !ok1 || !ok2 {
		return false, nil
	}
	return v.After(ref), map[string]string{"value": params[0]}
}

// ---------- helpers ------------------------------------------------------

func toString(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		return strconv.FormatBool(x)
	}
	return strconv.FormatFloat(toFloat64(v), 'f', -1, 64)
}

func toInt64(v any) int64 {
	switch x := v.(type) {
	case int:
		return int64(x)
	case int32:
		return int64(x)
	case int64:
		return x
	case float32:
		return int64(x)
	case float64:
		return int64(x)
	}
	return 0
}

func toFloat64(v any) float64 {
	switch x := v.(type) {
	case int:
		return float64(x)
	case int32:
		return float64(x)
	case int64:
		return float64(x)
	case float32:
		return float64(x)
	case float64:
		return x
	}
	return 0
}
