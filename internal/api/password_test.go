package api

import "testing"

func TestPasswordHashRoundTrip(t *testing.T) {
	phc, err := hashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	if len(phc) == 0 || phc[:9] != "$argon2id" {
		t.Fatalf("hash is not an argon2id PHC string: %q", phc)
	}
	if phc == hashPasswordMust(t, "correct horse battery staple") {
		// two hashes of the same password must differ (random salt)
		t.Fatal("salt reuse: two hashes of one password are identical")
	}
	if !verifyPassword(phc, "correct horse battery staple") {
		t.Fatal("verifyPassword rejected the right password")
	}
	if verifyPassword(phc, "wrong password") {
		t.Fatal("verifyPassword accepted a wrong password")
	}
}

func TestVerifyPasswordRejectsGarbageHash(t *testing.T) {
	for _, bad := range []string{"", "plaintext", "$argon2id$v=19$m=65536"} {
		if verifyPassword(bad, "x") {
			t.Fatalf("verifyPassword accepted malformed hash %q", bad)
		}
	}
}

func hashPasswordMust(t *testing.T, pw string) string {
	t.Helper()
	phc, err := hashPassword(pw)
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	return phc
}
