package auth

import "testing"

func TestNewSecretIsOpaqueBase64URL(t *testing.T) {
	a, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("two minted secrets are identical")
	}
	// 32 bytes in raw (unpadded) base64url is exactly 43 characters.
	if len(a) != 43 {
		t.Fatalf("secret length = %d, want 43", len(a))
	}
	for _, r := range a {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			t.Fatalf("secret has non-base64url character %q", r)
		}
	}
}

// SHA-256("abc") from the NIST test vectors — the digest form is a
// storage contract (sessions.token_hash, telegram tickets), so it is
// pinned against an independent source of truth.
func TestTokenDigestMatchesKnownVector(t *testing.T) {
	const want = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got := TokenDigest("abc"); got != want {
		t.Fatalf("TokenDigest(abc) = %s, want %s", got, want)
	}
}
