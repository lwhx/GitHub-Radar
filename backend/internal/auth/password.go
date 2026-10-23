package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

var ErrInvalidPasswordHash = errors.New("invalid password hash")

const (
	maxEncodedHashLength = 512
	maxHashMemory        = 128 * 1024
	maxHashIterations    = 5
	maxHashParallelism   = 4
	maxSaltLength        = 64
	maxKeyLength         = 64
)

type PasswordParams struct {
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

type passwordHasher struct {
	params PasswordParams
	random io.Reader
}

func newPasswordHasher(params PasswordParams, random io.Reader) *passwordHasher {
	return &passwordHasher{params: params, random: random}
}

func defaultPasswordHasher() *passwordHasher {
	return newPasswordHasher(PasswordParams{
		Memory:      64 * 1024,
		Iterations:  3,
		Parallelism: 2,
		SaltLength:  16,
		KeyLength:   32,
	}, rand.Reader)
}

func (h *passwordHasher) Hash(password string) (string, error) {
	salt := make([]byte, h.params.SaltLength)
	if _, err := io.ReadFull(h.random, salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, h.params.Iterations, h.params.Memory, h.params.Parallelism, h.params.KeyLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		h.params.Memory,
		h.params.Iterations,
		h.params.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func (h *passwordHasher) Verify(password, encoded string) (bool, error) {
	if len(encoded) > maxEncodedHashLength {
		return false, ErrInvalidPasswordHash
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, ErrInvalidPasswordHash
	}
	version, err := parsePHCUint(parts[2], "v=")
	if err != nil || version != argon2.Version {
		return false, ErrInvalidPasswordHash
	}
	paramParts := strings.Split(parts[3], ",")
	if len(paramParts) != 3 {
		return false, ErrInvalidPasswordHash
	}
	memory, err := parsePHCUint(paramParts[0], "m=")
	if err != nil {
		return false, ErrInvalidPasswordHash
	}
	iterations, err := parsePHCUint(paramParts[1], "t=")
	if err != nil {
		return false, ErrInvalidPasswordHash
	}
	parallelism, err := parsePHCUint(paramParts[2], "p=")
	if err != nil || memory == 0 || memory > maxHashMemory || iterations == 0 || iterations > maxHashIterations || parallelism == 0 || parallelism > maxHashParallelism {
		return false, ErrInvalidPasswordHash
	}
	params := PasswordParams{Memory: uint32(memory), Iterations: uint32(iterations), Parallelism: uint8(parallelism)}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) < 8 || len(salt) > maxSaltLength {
		return false, ErrInvalidPasswordHash
	}
	key, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(key) < 16 || len(key) > maxKeyLength {
		return false, ErrInvalidPasswordHash
	}
	candidate := argon2.IDKey([]byte(password), salt, params.Iterations, params.Memory, params.Parallelism, uint32(len(key)))
	return subtle.ConstantTimeCompare(candidate, key) == 1, nil
}

func parsePHCUint(value, prefix string) (uint64, error) {
	if !strings.HasPrefix(value, prefix) || len(value) == len(prefix) {
		return 0, ErrInvalidPasswordHash
	}
	parsed, err := strconv.ParseUint(strings.TrimPrefix(value, prefix), 10, 32)
	if err != nil {
		return 0, ErrInvalidPasswordHash
	}
	return parsed, nil
}
