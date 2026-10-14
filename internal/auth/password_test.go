package auth

import "testing"

func TestPasswordHashRoundTrip(t *testing.T) {
	phc, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if len(phc) == 0 || phc[:9] != "$argon2id" {
		t.Fatalf("hash is not an argon2id PHC string: %q", phc)
	}
	if phc == hashPasswordMust(t, "correct horse battery staple") {
		// two hashes of the same password must differ (random salt)
		t.Fatal("salt reuse: two hashes of one password are identical")
	}
	if !VerifyPassword(phc, "correct horse battery staple") {
		t.Fatal("VerifyPassword rejected the right password")
	}
	if VerifyPassword(phc, "wrong password") {
		t.Fatal("VerifyPassword accepted a wrong password")
	}
}

func TestVerifyPasswordRejectsGarbageHash(t *testing.T) {
	for _, bad := range []string{"", "plaintext", "$argon2id$v=19$m=65536"} {
		if VerifyPassword(bad, "x") {
			t.Fatalf("VerifyPassword accepted malformed hash %q", bad)
		}
	}
}

func TestCheckPasswordPolicyBounds(t *testing.T) {
	if err := CheckPasswordPolicy("1234567"); err == nil || err.Error() != "Choose a password of at least 8 characters." {
		t.Fatalf("7-character password: got %v", err)
	}
	if err := CheckPasswordPolicy("12345678"); err != nil {
		t.Fatalf("8-character password rejected: %v", err)
	}
	if err := CheckPasswordPolicy(make512()); err != nil {
		t.Fatalf("512-character password rejected: %v", err)
	}
	if err := CheckPasswordPolicy(make513()); err == nil || err.Error() != "Choose a password of at most 512 characters." {
		t.Fatalf("513-character password: got %v", err)
	}
}

func make512() string { return string(make([]byte, 512)) }
func make513() string { return string(make([]byte, 513)) }

func hashPasswordMust(t *testing.T, pw string) string {
	t.Helper()
	phc, err := HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	return phc
}
