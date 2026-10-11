package money

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"
)

func TestParseAcceptsExactForm(t *testing.T) {
	for _, s := range []string{"0", "1", "-1", "0.1", "123.456789", "-0.000001", "4611686018427387904"} {
		if _, err := Parse(s); err != nil {
			t.Errorf("Parse(%q): %v", s, err)
		}
	}
}

func TestParseRejects(t *testing.T) {
	for _, s := range []string{"", " 1", "1 ", "+1", "1e3", "1E3", ".", "1.", ".5", "0x10", "1,5", "١٢"} {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) should fail", s)
		}
	}
}

func TestFloorToTruncatesTowardZero(t *testing.T) {
	cases := []struct {
		in    string
		scale int32
		want  string
	}{
		{"1.999", 2, "1.99"},
		{"-1.999", 2, "-1.99"}, // truncate toward zero, not floor-below
		{"1", 8, "1"},
	}
	for _, c := range cases {
		got := FloorTo(MustParse(c.in), c.scale)
		if got.String() != c.want {
			t.Errorf("FloorTo(%s, %d) = %s, want %s", c.in, c.scale, got, c.want)
		}
	}
	// Format is the pinned-scale serialization; FloorTo drops trailing zeros.
	if got := Format(MustParse("0.0000001"), 6); got != "0.000000" {
		t.Errorf("Format(0.0000001, 6) = %q, want 0.000000", got)
	}
}

func TestFormatPinsScale(t *testing.T) {
	if got := Format(MustParse("1.5"), 8); got != "1.50000000" {
		t.Errorf("Format = %q, want 1.50000000", got)
	}
	if got := Format(MustParse("2.345"), 2); got != "2.34" {
		t.Errorf("Format = %q, want 2.34 (floored, not rounded)", got)
	}
}

func TestWithinScale(t *testing.T) {
	if !WithinScale(MustParse("1.23"), 2) {
		t.Error("1.23 should be within scale 2")
	}
	if WithinScale(MustParse("1.234"), 2) {
		t.Error("1.234 should exceed scale 2")
	}
}

func TestConformsAndTicks(t *testing.T) {
	tick := MustParse("0.1")
	lot := MustParse("0.001")
	if !Conforms(MustParse("123.4"), tick) {
		t.Error("123.4 conforms to tick 0.1")
	}
	if Conforms(MustParse("123.45"), tick) {
		t.Error("123.45 does not conform to tick 0.1")
	}
	if n, err := Ticks(MustParse("123.4"), tick); err != nil || n != 1234 {
		t.Errorf("Ticks(123.4, 0.1) = %d, %v; want 1234, nil", n, err)
	}
	if !Conforms(MustParse("0.007"), lot) {
		t.Error("0.007 conforms to lot 0.001")
	}
	if _, err := Ticks(MustParse("123.45"), tick); err == nil {
		t.Error("Ticks must reject non-conforming values")
	}
	if got := FromTicks(1234, tick); !got.Equal(MustParse("123.4")) {
		t.Errorf("FromTicks(1234, 0.1) = %s, want 123.4", got)
	}
}

func TestTicksOverflow(t *testing.T) {
	// tick so small that a plausible price exceeds int64 quanta
	tiny := decimal.New(1, -18)
	big := decimal.New(9223372036854775807, 0).Add(decimal.NewFromInt(1))
	if _, err := Ticks(big, tiny); err == nil {
		t.Error("Ticks must reject values that overflow int64 quanta")
	}
}

func TestFeeFloors(t *testing.T) {
	// buy 0.0001 BTC at 0.1% taker, BTC scale 8: 0.0001*0.001 = 0.0000001
	fee := Fee(MustParse("0.0001"), MustParse("0.001"), 8)
	if fee.String() != "0.0000001" {
		t.Errorf("fee = %s", fee)
	}
	// dust: 0.00001 * 0.001 = 0.00000001 at scale 8 → nonzero;
	// 0.000001 * 0.0001 = 0.0000000001 floors to zero at scale 8
	fee = Fee(MustParse("0.000001"), MustParse("0.0001"), 8)
	if !fee.IsZero() {
		t.Errorf("fee should floor to zero, got %s", fee)
	}
}

func TestTrimScale(t *testing.T) {
	if got := TrimScale(MustParse("1.500")); got != "1.5" {
		t.Errorf("TrimScale = %q", got)
	}
	if got := TrimScale(MustParse("2.000")); got != "2" {
		t.Errorf("TrimScale = %q", got)
	}
}

func TestParseErrorMentionsValue(t *testing.T) {
	_, err := Parse("1.5.2")
	if err == nil || !errors.Is(err, err) {
		t.Fatalf("unexpected err %v", err)
	}
}
