package auth

import "testing"

func TestHashPasswordRoundTrip(t *testing.T) {
	password := []byte("local-test-password")
	encoded, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	matched, err := VerifyPassword(encoded, password)
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Fatal("valid password rejected")
	}
}

func TestVerifyPasswordRejectsWrongPassword(t *testing.T) {
	encoded, err := HashPassword([]byte("local-test-password"))
	if err != nil {
		t.Fatal(err)
	}
	matched, err := VerifyPassword(encoded, []byte("different-test-password"))
	if err != nil {
		t.Fatal(err)
	}
	if matched {
		t.Fatal("wrong password accepted")
	}
}

func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	for _, encoded := range []string{
		"",
		"not-an-argon2-hash",
		"$argon2id$v=18$m=65536,t=3,p=2$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=invalid,t=3,p=2$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=65536,t=3,p=2$invalid!$aGFzaA",
	} {
		t.Run(encoded, func(t *testing.T) {
			if _, err := VerifyPassword(encoded, []byte("local-test-password")); err == nil {
				t.Fatal("malformed hash accepted")
			}
		})
	}
}
