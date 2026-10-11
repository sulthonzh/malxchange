package outbox

import (
	"testing"
	"time"
)

func TestBackoffGrowsAndCaps(t *testing.T) {
	base := time.Second
	var prev time.Duration
	for attempt := int64(0); attempt < 12; attempt++ {
		d := Backoff(base, 10, 42, attempt)
		if d < 750*time.Millisecond {
			t.Errorf("attempt %d: %s below jitter floor", attempt, d)
		}
		if d > 5*time.Minute+time.Minute {
			t.Errorf("attempt %d: %s above cap", attempt, d)
		}
		if attempt > 0 && d < prev/4 {
			t.Errorf("attempt %d: %s collapsed vs previous %s", attempt, d, prev)
		}
		if d > 5*time.Minute+125*time.Millisecond { // cap*1.25 jitter ceiling
			t.Errorf("attempt %d: %s exceeds cap with jitter", attempt, d)
		}
		prev = d
	}
}

func TestBackoffDeterministicPerSeed(t *testing.T) {
	a := Backoff(time.Second, 10, 7, 3)
	b := Backoff(time.Second, 10, 7, 3)
	if a != b {
		t.Errorf("same seed must give same delay: %s vs %s", a, b)
	}
	c := Backoff(time.Second, 10, 8, 3)
	if a == c {
		// Same delay across different seeds is not impossible, but with
		// 50% jitter range it is vanishingly unlikely.
		t.Logf("seed collision: %s", a)
	}
}

func TestBackoffNegativeAttemptsClamped(t *testing.T) {
	if d := Backoff(time.Second, 10, 1, -5); d < 750*time.Millisecond || d > 2*time.Second {
		t.Errorf("clamped backoff out of range: %s", d)
	}
}
