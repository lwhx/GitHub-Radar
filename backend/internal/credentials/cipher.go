package credentials

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

const ciphertextVersion = "v1"

var (
	ciphertextAAD        = []byte("github-radar:github-token:v1")
	ErrInvalidCiphertext = errors.New("invalid credential ciphertext")
)

type Cipher struct {
	aead   cipher.AEAD
	random io.Reader
}

func NewCipher(key []byte, random io.Reader) (*Cipher, error) {
	if len(key) != 32 {
		return nil, errors.New("credential encryption key must contain exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create credential cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create credential AEAD: %w", err)
	}
	if random == nil {
		random = rand.Reader
	}
	return &Cipher{aead: aead, random: random}, nil
}

func (cipher *Cipher) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, cipher.aead.NonceSize())
	if _, err := io.ReadFull(cipher.random, nonce); err != nil {
		return "", fmt.Errorf("generate credential nonce: %w", err)
	}
	sealed := cipher.aead.Seal(nonce, nonce, []byte(plaintext), ciphertextAAD)
	return ciphertextVersion + ":" + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (cipher *Cipher) Decrypt(value string) (string, error) {
	version, encoded, found := strings.Cut(value, ":")
	if !found || version != ciphertextVersion {
		return "", ErrInvalidCiphertext
	}
	sealed, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(sealed) < cipher.aead.NonceSize()+cipher.aead.Overhead() {
		return "", ErrInvalidCiphertext
	}
	nonce := sealed[:cipher.aead.NonceSize()]
	plaintext, err := cipher.aead.Open(nil, nonce, sealed[cipher.aead.NonceSize():], ciphertextAAD)
	if err != nil {
		return "", ErrInvalidCiphertext
	}
	return string(plaintext), nil
}
