package credentials

import (
	"bytes"
	"strings"
	"testing"
)

func TestCipherEncryptsTokenWithRandomNonceAndDecrypts(t *testing.T) {
	t.Parallel()

	key := bytes.Repeat([]byte{0x42}, 32)
	random := bytes.NewReader(append(bytes.Repeat([]byte{0x01}, 12), bytes.Repeat([]byte{0x02}, 12)...))
	cipher, err := NewCipher(key, random)
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	first, err := cipher.Encrypt("github_pat_secret-value")
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	second, err := cipher.Encrypt("github_pat_secret-value")
	if err != nil {
		t.Fatalf("Encrypt() second error = %v", err)
	}
	if first == second {
		t.Fatal("same token produced identical ciphertext")
	}
	if !strings.HasPrefix(first, "v1:") {
		t.Errorf("ciphertext = %q", first)
	}
	plaintext, err := cipher.Decrypt(first)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if plaintext != "github_pat_secret-value" {
		t.Errorf("plaintext = %q", plaintext)
	}
}
