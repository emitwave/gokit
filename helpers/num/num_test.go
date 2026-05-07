package num

import (
	"math"
	"strings"
	"testing"
)

func TestFormat(t *testing.T) {
	cases := []struct {
		n         float64
		precision int
		want      string
	}{
		{1234.567, 2, "1,234.57"},
		{1000, 0, "1,000"},
		{0, 2, "0.00"},
		{1234567.89, 2, "1,234,567.89"},
		{-1234.5, 1, "-1,234.5"},
		{0.5, -1, "0.5"},      // -1 = trim trailing zeros
		{1.10000, -1, "1.1"},
	}
	for _, c := range cases {
		got := Format(c.n, c.precision)
		if got != c.want {
			t.Errorf("Format(%v, %d): got %q, want %q", c.n, c.precision, got, c.want)
		}
	}
}

func TestTrim(t *testing.T) {
	if got := Trim(1.2300); got != "1.23" {
		t.Errorf("got %q", got)
	}
	if got := Trim(100.0); got != "100" {
		t.Errorf("got %q", got)
	}
}

func TestCurrency(t *testing.T) {
	cases := []struct {
		n    float64
		code string
		want string
	}{
		{1234.5, "USD", "$1,234.50"},
		{99.99, "EUR", "€99.99"},
		{1500, "JPY", "¥1,500"}, // zero-decimal
		{2500, "NGN", "₦2,500.00"},
		{500, "GBP", "£500.00"},
	}
	for _, c := range cases {
		got := Currency(c.n, c.code)
		if got != c.want {
			t.Errorf("Currency(%v, %q): got %q, want %q", c.n, c.code, got, c.want)
		}
	}
}

func TestCurrencyDefault(t *testing.T) {
	defer SetDefaultCurrency("USD") // restore after test
	SetDefaultCurrency("NGN")
	got := Currency(1000, "")
	if got != "₦1,000.00" {
		t.Errorf("default currency: got %q", got)
	}
}

func TestWithCurrency(t *testing.T) {
	prev := DefaultCurrency()
	WithCurrency("EUR", func() {
		if DefaultCurrency() != "EUR" {
			t.Errorf("WithCurrency did not switch: got %q", DefaultCurrency())
		}
	})
	if DefaultCurrency() != prev {
		t.Errorf("WithCurrency did not restore: got %q want %q", DefaultCurrency(), prev)
	}
}

func TestFileSize(t *testing.T) {
	cases := []struct {
		bytes     int64
		precision int
		want      string
	}{
		{0, 0, "0 B"},
		{500, 0, "500 B"},
		{1024, 0, "1 KB"},
		{1024 * 1024, 0, "1 MB"},
		{1024 * 1024 * 1024, 0, "1 GB"},
		{1500, 1, "1.5 KB"},
		{1500 * 1024, 1, "1.5 MB"},
		{3 * 1024 * 1024 * 1024 / 2, 2, "1.50 GB"}, // exactly 1.5 GB in binary units
	}
	for _, c := range cases {
		got := FileSize(c.bytes, c.precision)
		if got != c.want {
			t.Errorf("FileSize(%d, %d): got %q, want %q", c.bytes, c.precision, got, c.want)
		}
	}
}

func TestAbbreviate(t *testing.T) {
	cases := []struct {
		n         float64
		precision int
		want      string
	}{
		{500, 0, "500"},
		{1000, 0, "1K"},
		{1500, 1, "1.5K"},
		{1_000_000, 0, "1M"},
		{2_500_000, 1, "2.5M"},
		{1_000_000_000, 0, "1B"},
		{1_500_000_000_000, 1, "1.5T"},
	}
	for _, c := range cases {
		got := Abbreviate(c.n, c.precision)
		if got != c.want {
			t.Errorf("Abbreviate(%v, %d): got %q, want %q", c.n, c.precision, got, c.want)
		}
	}
}

func TestForHumans(t *testing.T) {
	cases := []struct {
		n    float64
		want string
	}{
		{500, "500"},
		{1000, "1 thousand"},
		{2_500_000, "2.5 million"},
		{1_000_000_000, "1 billion"},
	}
	for _, c := range cases {
		got := ForHumans(c.n, -1)
		if got != c.want {
			t.Errorf("ForHumans(%v): got %q, want %q", c.n, got, c.want)
		}
	}
}

func TestOrdinal(t *testing.T) {
	cases := map[int]string{
		1: "1st", 2: "2nd", 3: "3rd", 4: "4th",
		11: "11th", 12: "12th", 13: "13th",
		21: "21st", 22: "22nd", 23: "23rd",
		101: "101st", 111: "111th", 112: "112th", 113: "113th",
	}
	for n, want := range cases {
		if got := Ordinal(n); got != want {
			t.Errorf("Ordinal(%d): got %q, want %q", n, got, want)
		}
	}
}

func TestSpellOrdinal(t *testing.T) {
	cases := map[int]string{
		0: "zeroth", 1: "first", 2: "second", 5: "fifth", 12: "twelfth",
		20: "twentieth",
		21: "21st", // beyond 20 → Ordinal fallback
	}
	for n, want := range cases {
		if got := SpellOrdinal(n); got != want {
			t.Errorf("SpellOrdinal(%d): got %q, want %q", n, got, want)
		}
	}
}

func TestPercentage(t *testing.T) {
	if got := Percentage(42.5, 1); got != "42.5%" {
		t.Errorf("got %q", got)
	}
	if got := Percentage(99, 0); got != "99%" {
		t.Errorf("got %q", got)
	}
	if got := Percentage(50, 2); got != "50.00%" {
		t.Errorf("got %q", got)
	}
}

func TestClamp(t *testing.T) {
	if got := Clamp(5, 1, 10); got != 5 {
		t.Errorf("inside range: %d", got)
	}
	if got := Clamp(-5, 0, 10); got != 0 {
		t.Errorf("below: %d", got)
	}
	if got := Clamp(99, 0, 10); got != 10 {
		t.Errorf("above: %d", got)
	}
	// Float
	if got := Clamp(1.5, 0.0, 1.0); got != 1.0 {
		t.Errorf("float clamp: %v", got)
	}
}

func TestParseInt(t *testing.T) {
	if got := ParseInt("42", 10, -1); got != 42 {
		t.Errorf("got %d", got)
	}
	if got := ParseInt("0x2a", 0, -1); got != 42 {
		t.Errorf("hex auto: %d", got)
	}
	if got := ParseInt("not-a-number", 10, 99); got != 99 {
		t.Errorf("bad input: %d", got)
	}
	if got := ParseInt(" 100 ", 10, 0); got != 100 {
		t.Errorf("whitespace: %d", got)
	}
}

func TestParseFloat(t *testing.T) {
	if got := ParseFloat("3.14", 0); got != 3.14 {
		t.Errorf("got %v", got)
	}
	if got := ParseFloat("1,234.56", 0); got != 1234.56 {
		t.Errorf("with commas: %v", got)
	}
	if got := ParseFloat("bad", 99.0); got != 99.0 {
		t.Errorf("bad input: %v", got)
	}
}

func TestNaNAndInfinity(t *testing.T) {
	// Make sure these don't panic.
	if !strings.Contains(Format(math.Inf(1), 0), "∞") {
		t.Error("infinity not handled")
	}
	if !strings.Contains(Format(math.NaN(), 0), "NaN") {
		t.Error("NaN not handled")
	}
}

func TestSetAndUseDefaultLocale(t *testing.T) {
	defer SetDefaultLocale("en-US")
	SetDefaultLocale("fr-FR")
	if DefaultLocale() != "fr-FR" {
		t.Errorf("default locale: %q", DefaultLocale())
	}
}

func TestWithLocale(t *testing.T) {
	prev := DefaultLocale()
	WithLocale("ja-JP", func() {
		if DefaultLocale() != "ja-JP" {
			t.Error("did not switch")
		}
	})
	if DefaultLocale() != prev {
		t.Errorf("did not restore: got %q want %q", DefaultLocale(), prev)
	}
}
