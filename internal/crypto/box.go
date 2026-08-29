package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

const formatVersion byte = 1

type Box struct {
	aead cipher.AEAD
}

func NewBox(key []byte) (*Box, error) {
	if len(key) != 32 {
		return nil, errors.New("encryption key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create authenticated cipher: %w", err)
	}
	return &Box{aead: aead}, nil
}

func (box *Box) Seal(plaintext, additionalData []byte) ([]byte, error) {
	nonce := make([]byte, box.aead.NonceSize())
	if _, err := io.ReadFull(cryptorand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	output := make([]byte, 1, 1+len(nonce)+len(plaintext)+box.aead.Overhead())
	output[0] = formatVersion
	output = append(output, nonce...)
	output = box.aead.Seal(output, nonce, plaintext, additionalData)
	return output, nil
}

func (box *Box) Open(ciphertext, additionalData []byte) ([]byte, error) {
	minimum := 1 + box.aead.NonceSize() + box.aead.Overhead()
	if len(ciphertext) < minimum || ciphertext[0] != formatVersion {
		return nil, errors.New("invalid encrypted value")
	}
	nonceEnd := 1 + box.aead.NonceSize()
	plaintext, err := box.aead.Open(nil, ciphertext[1:nonceEnd], ciphertext[nonceEnd:], additionalData)
	if err != nil {
		return nil, errors.New("encrypted value authentication failed")
	}
	return plaintext, nil
}

func RandomURLSafe(bytes int) (string, error) {
	value := make([]byte, bytes)
	if _, err := io.ReadFull(cryptorand.Reader, value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func Digest(value string) []byte {
	digest := sha256.Sum256([]byte(value))
	return digest[:]
}

func EqualDigest(digest []byte, value string) bool {
	actual := Digest(value)
	return len(digest) == len(actual) && subtle.ConstantTimeCompare(digest, actual) == 1
}
