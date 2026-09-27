package security

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"

	"golang.org/x/crypto/bcrypt"
)

func GenerateServiceKey(keyBytes, hashCost int) (string, string, []byte, error) {
	secret := make([]byte, keyBytes)
	if _, err := rand.Read(secret); err != nil {
		return "", "", nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(secret)
	clear(secret)
	verifier, err := HashServiceKey(token, hashCost)
	if err != nil {
		return "", "", nil, err
	}
	identifier := make([]byte, keyBytes)
	if _, err := rand.Read(identifier); err != nil {
		clear(verifier)
		return "", "", nil, err
	}
	id := hex.EncodeToString(identifier)
	clear(identifier)
	return id, token, verifier, nil
}

func HashServiceKey(token string, cost int) ([]byte, error) {
	return bcrypt.GenerateFromPassword([]byte(token), cost)
}

func CompareServiceKey(verifier []byte, token string) bool {
	return bcrypt.CompareHashAndPassword(verifier, []byte(token)) == nil
}
