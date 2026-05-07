// Package num ports Laravel's Number::* helpers: formatting, currency,
// file-size formatting, abbreviation, ordinals, parsing, and percentage.
//
// The package keeps state-free APIs by default (each call takes its own
// locale/currency parameters), with package-level "default locale" and
// "default currency" registers for convenience — see SetDefaultLocale
// and SetDefaultCurrency.
//
// Locale support is intentionally light: this package handles English
// formatting cleanly and accepts any BCP 47 locale tag for currency
// identification. Full locale-aware formatting (e.g. comma vs. period
// as decimal separator across all locales) requires golang.org/x/text;
// callers who need that should use the helpers there directly. We keep
// num light by default so it stays a "go get" away with no large deps.
package num

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
)

// =========================================================================
// Defaults — package-level locale and currency registers
// =========================================================================

var (
	defaultsMu      sync.RWMutex
	defaultLocale   = "en-US"
	defaultCurrency = "USD"
)

// DefaultLocale returns the locale used when callers don't pass one
// explicitly. Defaults to "en-US".
func DefaultLocale() string {
	defaultsMu.RLock()
	defer defaultsMu.RUnlock()
	return defaultLocale
}

// SetDefaultLocale (Laravel: Number::useLocale) registers a new default
// locale. Affects subsequent calls that don't pass an explicit locale.
func SetDefaultLocale(locale string) {
	defaultsMu.Lock()
	defaultLocale = locale
	defaultsMu.Unlock()
}

// WithLocale runs fn with the given locale temporarily set as default,
// then restores the prior default. Mirrors Number::withLocale.
func WithLocale(locale string, fn func()) {
	defaultsMu.Lock()
	prev := defaultLocale
	defaultLocale = locale
	defaultsMu.Unlock()

	defer func() {
		defaultsMu.Lock()
		defaultLocale = prev
		defaultsMu.Unlock()
	}()
	fn()
}

// DefaultCurrency returns the currency code used when callers don't pass
// one explicitly. Defaults to "USD".
func DefaultCurrency() string {
	defaultsMu.RLock()
	defer defaultsMu.RUnlock()
	return defaultCurrency
}

// SetDefaultCurrency registers a new default currency.
func SetDefaultCurrency(code string) {
	defaultsMu.Lock()
	defaultCurrency = code
	defaultsMu.Unlock()
}

// WithCurrency runs fn with the given currency temporarily set as default.
func WithCurrency(code string, fn func()) {
	defaultsMu.Lock()
	prev := defaultCurrency
	defaultCurrency = code
	defaultsMu.Unlock()

	defer func() {
		defaultsMu.Lock()
		defaultCurrency = prev
		defaultsMu.Unlock()
	}()
	fn()
}

// =========================================================================
// Formatting
// =========================================================================

// Format returns n as a string with thousands separators and a fixed
// number of decimal places. precision = -1 trims trailing zeros (no
// fixed point). Mirrors Number::format with the en-US locale by default.
//
//	num.Format(1234567.89, 2)  => "1,234,567.89"
//	num.Format(1000, 0)        => "1,000"
//	num.Format(0.5, -1)        => "0.5"
func Format(n float64, precision int) string {
	if math.IsNaN(n) {
		return "NaN"
	}
	if math.IsInf(n, 0) {
		if n > 0 {
			return "∞"
		}
		return "-∞"
	}

	// Format the magnitude as plain decimal, then add separators.
	var s string
	if precision < 0 {
		s = strconv.FormatFloat(n, 'f', -1, 64)
	} else {
		s = strconv.FormatFloat(n, 'f', precision, 64)
	}

	// Split sign / integer / fractional.
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	}
	intPart, fracPart, hasFrac := strings.Cut(s, ".")

	intPart = withThousandsSep(intPart, ',')

	out := intPart
	if hasFrac {
		out += "." + fracPart
	}
	if neg {
		out = "-" + out
	}
	return out
}

func withThousandsSep(intStr string, sep byte) string {
	n := len(intStr)
	if n <= 3 {
		return intStr
	}
	// Build from the end inserting sep every 3 digits.
	var b strings.Builder
	first := n % 3
	if first > 0 {
		b.WriteString(intStr[:first])
	}
	for i := first; i < n; i += 3 {
		if b.Len() > 0 {
			b.WriteByte(sep)
		}
		b.WriteString(intStr[i : i+3])
	}
	return b.String()
}

// Trim formats n with no trailing zeros (Number::trim equivalent).
// `1.2300` → `"1.23"`, `100.0` → `"100"`.
func Trim(n float64) string {
	return strconv.FormatFloat(n, 'f', -1, 64)
}

// =========================================================================
// Currency
// =========================================================================

// Currency formats n as currency. The currency code controls the symbol
// (USD → $, EUR → €, NGN → ₦, GBP → £, JPY → ¥) and JPY-like currencies
// get zero decimal places. Other locales use code prefix.
//
//	num.Currency(1234.5, "")              // uses DefaultCurrency, en-US format
//	num.Currency(99.99, "EUR")            // "€99.99"
//	num.Currency(1500, "JPY")             // "¥1,500"
//	num.Currency(2500, "NGN")             // "₦2,500.00"
func Currency(n float64, code string) string {
	if code == "" {
		code = DefaultCurrency()
	}
	symbol, decimals := currencyFormat(code)
	return symbol + Format(n, decimals)
}

// currencyFormat returns (symbol, default decimals) for a currency code.
// Unknown codes get the code itself as a prefix and 2 decimals.
func currencyFormat(code string) (string, int) {
	switch strings.ToUpper(code) {
	case "USD":
		return "$", 2
	case "EUR":
		return "€", 2
	case "GBP":
		return "£", 2
	case "JPY", "KRW":
		return "¥", 0 // zero-decimal currencies
	case "NGN":
		return "₦", 2
	case "ZAR":
		return "R", 2
	case "CNY":
		return "¥", 2 // RMB shares the symbol with JPY in many fonts
	case "INR":
		return "₹", 2
	case "AUD", "CAD", "NZD", "SGD":
		// "$"" prefix with code in front
		return code + " $", 2
	case "CHF":
		return "CHF ", 2
	default:
		return code + " ", 2
	}
}

// =========================================================================
// File size
// =========================================================================

// FileSize returns a human-readable string for a byte count using
// binary prefixes (KB = 1024, MB = 1024², ...). precision = -1 trims
// trailing zeros automatically; a non-negative value rounds to that many
// decimal places.
//
//	num.FileSize(1024, 0)         => "1 KB"
//	num.FileSize(1500*1024, 1)    => "1.5 MB"
//	num.FileSize(0, 0)            => "0 B"
func FileSize(bytes int64, precision int) string {
	const (
		_  = iota
		kb = 1 << (10 * iota)
		mb
		gb
		tb
		pb
	)
	abs := bytes
	if abs < 0 {
		abs = -abs
	}
	switch {
	case abs >= pb:
		return formatSize(float64(bytes)/float64(pb), precision) + " PB"
	case abs >= tb:
		return formatSize(float64(bytes)/float64(tb), precision) + " TB"
	case abs >= gb:
		return formatSize(float64(bytes)/float64(gb), precision) + " GB"
	case abs >= mb:
		return formatSize(float64(bytes)/float64(mb), precision) + " MB"
	case abs >= kb:
		return formatSize(float64(bytes)/float64(kb), precision) + " KB"
	default:
		return strconv.FormatInt(bytes, 10) + " B"
	}
}

func formatSize(v float64, precision int) string {
	if precision < 0 {
		// Auto: 1 decimal if not integer, 0 if integer.
		if v == math.Floor(v) {
			return strconv.FormatFloat(v, 'f', 0, 64)
		}
		return strconv.FormatFloat(v, 'f', 1, 64)
	}
	return strconv.FormatFloat(v, 'f', precision, 64)
}

// =========================================================================
// Abbreviation and "for humans"
// =========================================================================

// Abbreviate returns a compact form: "1K", "1.5M", "2.3B". precision =
// -1 trims trailing zeros automatically. Mirrors Number::abbreviate.
func Abbreviate(n float64, precision int) string {
	abs := math.Abs(n)
	switch {
	case abs >= 1e12:
		return formatSize(n/1e12, precision) + "T"
	case abs >= 1e9:
		return formatSize(n/1e9, precision) + "B"
	case abs >= 1e6:
		return formatSize(n/1e6, precision) + "M"
	case abs >= 1e3:
		return formatSize(n/1e3, precision) + "K"
	default:
		return Format(n, precision)
	}
}

// ForHumans returns an abbreviation in words: "1 thousand", "5 million",
// "2.5 billion". Mirrors Number::forHumans.
func ForHumans(n float64, precision int) string {
	abs := math.Abs(n)
	switch {
	case abs >= 1e12:
		return formatSize(n/1e12, precision) + " trillion"
	case abs >= 1e9:
		return formatSize(n/1e9, precision) + " billion"
	case abs >= 1e6:
		return formatSize(n/1e6, precision) + " million"
	case abs >= 1e3:
		return formatSize(n/1e3, precision) + " thousand"
	default:
		return Format(n, precision)
	}
}

// =========================================================================
// Ordinals
// =========================================================================

// Ordinal returns "1st", "2nd", "3rd", "4th", etc. (English only).
func Ordinal(n int) string {
	suffix := "th"
	switch {
	case n%100 >= 11 && n%100 <= 13:
		suffix = "th"
	case n%10 == 1:
		suffix = "st"
	case n%10 == 2:
		suffix = "nd"
	case n%10 == 3:
		suffix = "rd"
	}
	return strconv.Itoa(n) + suffix
}

// SpellOrdinal returns "first", "second", "third", ... up to "twentieth".
// Beyond 20, falls back to Ordinal(n) (e.g. "21st"). English only.
func SpellOrdinal(n int) string {
	words := []string{
		"zeroth", "first", "second", "third", "fourth", "fifth", "sixth",
		"seventh", "eighth", "ninth", "tenth",
		"eleventh", "twelfth", "thirteenth", "fourteenth", "fifteenth",
		"sixteenth", "seventeenth", "eighteenth", "nineteenth", "twentieth",
	}
	if n >= 0 && n < len(words) {
		return words[n]
	}
	return Ordinal(n)
}

// =========================================================================
// Percentage
// =========================================================================

// Percentage formats n as a percentage. n is in 0–100 range (NOT 0–1)
// to match Laravel's behavior. precision = -1 trims trailing zeros.
//
//	num.Percentage(42.5, 1)       => "42.5%"
//	num.Percentage(99, 0)         => "99%"
func Percentage(n float64, precision int) string {
	return Format(n, precision) + "%"
}

// =========================================================================
// Clamp
// =========================================================================

// Clamp returns v constrained to [min, max]. Generic over any ordered
// numeric type.
func Clamp[T ~int | ~int8 | ~int16 | ~int32 | ~int64 |
	~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 |
	~float32 | ~float64](v, min, max T) T {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// =========================================================================
// Parsing
// =========================================================================

// ParseInt parses s as an int in the given base (0 = auto-detect from
// "0x", "0o", "0b" prefix). Returns def if parsing fails. Mirrors
// Number::parseInt with a default-on-error behavior.
func ParseInt(s string, base int, def int64) int64 {
	if s == "" {
		return def
	}
	v, err := strconv.ParseInt(strings.TrimSpace(s), base, 64)
	if err != nil {
		return def
	}
	return v
}

// ParseFloat parses s as a float. Accepts thousands separators (commas)
// and trims whitespace. Returns def on failure.
func ParseFloat(s string, def float64) float64 {
	if s == "" {
		return def
	}
	cleaned := strings.ReplaceAll(strings.TrimSpace(s), ",", "")
	v, err := strconv.ParseFloat(cleaned, 64)
	if err != nil {
		return def
	}
	return v
}

// =========================================================================
// Errors
// =========================================================================

// ErrInvalidNumber is reserved for future use by parsing helpers that
// want to surface a typed error.
var ErrInvalidNumber = errors.New("num: invalid number")

// describeProblem is a tiny formatter used in a few error paths.
func describeProblem(what string, raw any) string {
	return fmt.Sprintf("num: %s: %v", what, raw)
}
