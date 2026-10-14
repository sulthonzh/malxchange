package auth

import (
	"testing"
	"time"
)

// The mint arithmetic behind the two session models: pure sliding (no
// absolute — member api) vs absolute+idle (Employee sessions,
// ADR-0012: 12 h + 30 min). MintSession writes the widest window the
// policy allows; ResolveSession only narrows.
func TestSessionPolicyMintExpiry(t *testing.T) {
	cases := []struct {
		name string
		p    SessionPolicy
		want time.Duration
	}{
		{"pure slide uses the idle window", SessionPolicy{Idle: 7 * 24 * time.Hour}, 7 * 24 * time.Hour},
		{"absolute caps when it is the wider window", SessionPolicy{Absolute: 12 * time.Hour, Idle: 30 * time.Minute}, 12 * time.Hour},
		{"idle wins when wider than absolute", SessionPolicy{Absolute: time.Hour, Idle: 2 * time.Hour}, 2 * time.Hour},
		{"no absolute means the idle window", SessionPolicy{Absolute: 0, Idle: 30 * time.Minute}, 30 * time.Minute},
		{"negative absolute is no absolute", SessionPolicy{Absolute: -time.Hour, Idle: 30 * time.Minute}, 30 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.p.mintExpiry(); got != tc.want {
				t.Fatalf("mintExpiry(%+v) = %v, want %v", tc.p, got, tc.want)
			}
		})
	}
}
