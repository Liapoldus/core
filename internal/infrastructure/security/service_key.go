package security

import "golang.org/x/crypto/bcrypt"

func HashServiceKey(token string, cost int) ([]byte, error) {
	return bcrypt.GenerateFromPassword([]byte(token), cost)
}

func CompareServiceKey(verifier []byte, token string) bool {
	return bcrypt.CompareHashAndPassword(verifier, []byte(token)) == nil
}
