package crypto

import (
	"bytes"
	"testing"
)

func TestBoxRoundTripAndAuthentication(t *testing.T) {
	box, err := NewBox(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := box.Seal([]byte("refresh-secret"), []byte("account:one"))
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := box.Open(ciphertext, []byte("account:one"))
	if err != nil {
		t.Fatal(err)
	}
	if string(plaintext) != "refresh-secret" {
		t.Fatalf("unexpected plaintext %q", plaintext)
	}
	if _, err := box.Open(ciphertext, []byte("account:two")); err == nil {
		t.Fatal("expected additional-data authentication failure")
	}
	if bytes.Contains(ciphertext, []byte("refresh-secret")) {
		t.Fatal("ciphertext contains plaintext")
	}
}
