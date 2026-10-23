package auth

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	hasher := newPasswordHasher(PasswordParams{
		Memory:      64,
		Iterations:  1,
		Parallelism: 1,
		SaltLength:  16,
		KeyLength:   32,
	}, bytes.NewReader(bytes.Repeat([]byte{0x42}, 16)))

	hash, err := hasher.Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}
	if hash == "" {
		t.Fatal("Hash() returned an empty PHC string")
	}

	matched, err := hasher.Verify("correct horse battery staple", hash)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if !matched {
		t.Fatal("Verify() matched = false, want true")
	}
}

func TestPasswordHashRejectsWrongPassword(t *testing.T) {
	hasher := newPasswordHasher(PasswordParams{
		Memory: 64, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32,
	}, bytes.NewReader(bytes.Repeat([]byte{0x43}, 16)))
	hash, err := hasher.Hash("correct horse battery staple")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}

	matched, err := hasher.Verify("wrong password", hash)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if matched {
		t.Fatal("Verify() matched = true, want false")
	}
}

func TestPasswordHashRejectsMalformedAndExcessiveParameters(t *testing.T) {
	hasher := defaultPasswordHasher()
	encoded := base64.RawStdEncoding.EncodeToString([]byte("0123456789abcdef"))
	key := base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	tests := []string{
		"not-a-phc-string",
		"x$argon2id$v=19$m=65536,t=3,p=2$" + encoded + "$" + key,
		"$argon2id$v=19junk$m=65536,t=3,p=2$" + encoded + "$" + key,
		"$argon2id$v=19$m=+65536,t=3,p=2$" + encoded + "$" + key,
		"$argon2id$v=19$m=131073,t=3,p=2$" + encoded + "$" + key,
		"$argon2id$v=19$m=65536,t=6,p=2$" + encoded + "$" + key,
		"$argon2id$v=19$m=65536,t=3,p=5$" + encoded + "$" + key,
		"$argon2id$v=19$m=65536,t=3,p=2$" + base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 65)) + "$" + key,
		"$argon2id$v=19$m=65536,t=3,p=2$" + encoded + "$" + base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 65)),
		strings.Repeat("x", 513),
	}
	for _, encodedHash := range tests {
		matched, err := hasher.Verify("password never echoed", encodedHash)
		if matched || !errors.Is(err, ErrInvalidPasswordHash) {
			t.Errorf("Verify(%q) = (%v, %v), want (false, ErrInvalidPasswordHash)", encodedHash, matched, err)
		}
		if err != nil && (strings.Contains(err.Error(), "password never echoed") || strings.Contains(err.Error(), encodedHash)) {
			t.Errorf("Verify() error leaked sensitive input: %v", err)
		}
	}
}

func TestPasswordHasherDefaultsAreSecure(t *testing.T) {
	params := defaultPasswordHasher().params
	if params.Memory != 64*1024 || params.Iterations != 3 || params.Parallelism != 2 || params.SaltLength != 16 || params.KeyLength != 32 {
		t.Fatalf("default parameters = %+v", params)
	}
}

func TestPasswordHashReturnsRandomSourceErrorWithoutLeakingPassword(t *testing.T) {
	hasher := newPasswordHasher(PasswordParams{
		Memory: 64, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32,
	}, errReader{})
	password := "password must stay private"
	_, err := hasher.Hash(password)
	if err == nil {
		t.Fatal("Hash() error = nil, want random source error")
	}
	if strings.Contains(err.Error(), password) {
		t.Fatalf("Hash() error leaked password: %v", err)
	}
}
