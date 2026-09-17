package password

import "testing"

func TestBcryptRoundTripAndDummyHash(t *testing.T) {
	hasher := Bcrypt{}
	hash, err := hasher.Hash([]byte("correct-password"))
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	if hash == "correct-password" {
		t.Fatal("Hash() returned plaintext")
	}
	if err := hasher.Compare(hash, []byte("correct-password")); err != nil {
		t.Fatalf("Compare(correct) error = %v", err)
	}
	if err := hasher.Compare(hash, []byte("wrong-password")); err == nil {
		t.Fatal("Compare(wrong) error = nil")
	}
}
