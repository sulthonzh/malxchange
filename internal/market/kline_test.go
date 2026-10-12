package market

import (
	"errors"
	"testing"
)

// TestParseInterval pins the stored-interval vocabulary: exactly the eight
// stored intervals parse, anything else (including legacy 1m-only aliases
// and empty) is the named errBadInterval, so REST answers 400 instead of an
// empty success (ticket #31).
func TestParseInterval(t *testing.T) {
	t.Parallel()
	for _, iv := range StoredIntervals {
		got, err := ParseInterval(string(iv))
		if err != nil {
			t.Errorf("ParseInterval(%q): %v", iv, err)
			continue
		}
		if got != iv {
			t.Errorf("ParseInterval(%q) = %q", iv, got)
		}
	}
	for _, raw := range []string{"", "2m", "1M", "60m", "24h", "7d", "week", "1mm"} {
		if _, err := ParseInterval(raw); !errors.Is(err, errBadInterval) {
			t.Errorf("ParseInterval(%q) err = %v, want errBadInterval", raw, err)
		}
	}
}
