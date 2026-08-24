package auth

import (
	"testing"
	"time"
)

func TestGenerateTOTPSecretUsesTwentyRandomBytes(t *testing.T) {
	first, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 20 || len(second) != 20 {
		t.Fatalf("secret lengths = %d, %d", len(first), len(second))
	}
	if string(first) == string(second) {
		t.Fatal("generated secrets are identical")
	}
}

func TestValidateTOTPUsesRFC6238Window(t *testing.T) {
	secret := []byte("12345678901234567890")
	if !ValidateTOTP(secret, "287082", time.Unix(59, 0)) {
		t.Fatal("RFC 6238 vector rejected")
	}
	if !ValidateTOTP(secret, "287082", time.Unix(89, 0)) {
		t.Fatal("previous time step rejected")
	}
	if ValidateTOTP(secret, "287082", time.Unix(119, 0)) {
		t.Fatal("code outside the validation window accepted")
	}
}

func TestValidateTOTPRejectsMalformedCode(t *testing.T) {
	secret := []byte("12345678901234567890")
	for _, code := range []string{"", "12345", "1234567", "12a456"} {
		if ValidateTOTP(secret, code, time.Unix(59, 0)) {
			t.Fatalf("malformed code of length %d accepted", len(code))
		}
	}
}
