package api

import (
	"testing"
	"time"
)

// RFC 6238 Appendix B test vectors (SHA-1, secret "12345678901234567890"
// in base32 GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ).
func TestTOTPCodeMatchesRFC6238Vectors(t *testing.T) {
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	cases := []struct {
		unix int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1234567890, "005924"},
	}
	for _, tc := range cases {
		got, err := totpCode(secret, time.Unix(tc.unix, 0).UTC())
		if err != nil {
			t.Fatalf("totpCode(%d): %v", tc.unix, err)
		}
		if got != tc.want {
			t.Errorf("totpCode(%d) = %s, want %s", tc.unix, got, tc.want)
		}
	}
}

func TestVerifyTOTPAcceptsNeighboringSteps(t *testing.T) {
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	now := time.Unix(1234567890, 0).UTC()
	code, err := totpCode(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	if !verifyTOTP(secret, code, now) {
		t.Fatal("current step rejected")
	}
	if !verifyTOTP(secret, code, now.Add(20*time.Second)) {
		t.Fatal("same-step later second rejected")
	}
	if verifyTOTP(secret, "000000", now) && code != "000000" {
		t.Fatal("wrong code accepted")
	}
	if verifyTOTP(secret, "12345", now) {
		t.Fatal("short code accepted")
	}
}
