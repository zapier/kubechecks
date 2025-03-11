package pkg

import (
	"time"

	"github.com/golang-jwt/jwt/v4"
)

func CreateJWT(pkey string, issuer string) (string, error) {
	signingKey := []byte(pkey)

	// Create the Claims
	claims := &jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(10 * time.Minute)),
		Issuer:    issuer,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	ss, err := token.SignedString(signingKey)
	return ss, err
}
