package pkg

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

func CreateJWT(pkey string, issuer string) (string, error) {
	// Parse the PEM-encoded private key
	block, _ := pem.Decode([]byte(pkey))
	if block == nil {
		return "", errors.New("key is invalid")
	}

	var privateKey *rsa.PrivateKey
	var err error

	// Try PKCS1 format first
	if block.Type == "RSA PRIVATE KEY" {
		privateKey, err = x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return "", errors.New("key is invalid")
		}
	} else if block.Type == "PRIVATE KEY" {
		// Try PKCS8 format
		parsedKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return "", errors.New("key is invalid")
		}
		
		var ok bool
		privateKey, ok = parsedKey.(*rsa.PrivateKey)
		if !ok {
			return "", errors.New("key is invalid")
		}
	} else {
		return "", errors.New("key is invalid")
	}

	// Create the Claims
	claims := &jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(10 * time.Minute)),
		Issuer:    issuer,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	ss, err := token.SignedString(privateKey)
	return ss, err
}
