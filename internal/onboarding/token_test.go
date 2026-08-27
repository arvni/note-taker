package onboarding

import "testing"

func TestGenerateTokenHashesConsistently(t *testing.T) {
	raw, hash, err := GenerateToken(48)
	if err != nil {
		t.Fatal(err)
	}
	if raw == "" {
		t.Fatal("empty raw token")
	}
	if !ConstantTimeEqual(hash, HashToken(raw)) {
		t.Fatal("stored hash does not match recomputed hash")
	}
}

func TestGenerateTokenRejectsLowEntropy(t *testing.T) {
	if _, _, err := GenerateToken(16); err == nil {
		t.Fatal("expected error for 16-byte token")
	}
}

func TestTokensAreUnique(t *testing.T) {
	a, _, _ := GenerateToken(32)
	b, _, _ := GenerateToken(32)
	if a == b {
		t.Fatal("two generated tokens collided")
	}
}
