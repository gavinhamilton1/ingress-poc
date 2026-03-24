package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// KeyPair holds an EC P-256 key pair along with its JWK representation.
type KeyPair struct {
	PrivateKey *ecdsa.PrivateKey
	PublicKey  *ecdsa.PublicKey
	JWK        map[string]string
	Kid        string
}

// generateKeyPair creates a new ECDSA P-256 key pair and its JWK representation.
func generateKeyPair(kid string) *KeyPair {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(fmt.Sprintf("failed to generate EC key: %v", err))
	}
	pub := &priv.PublicKey

	jwk := ecPublicKeyToJWK(pub, kid)

	return &KeyPair{
		PrivateKey: priv,
		PublicKey:  pub,
		JWK:        jwk,
		Kid:        kid,
	}
}

// ecPublicKeyToJWK converts an ECDSA public key to a JWK map.
func ecPublicKeyToJWK(pub *ecdsa.PublicKey, kid string) map[string]string {
	x := base64URLEncode(pub.X.Bytes(), 32)
	y := base64URLEncode(pub.Y.Bytes(), 32)
	return map[string]string{
		"kty": "EC",
		"crv": "P-256",
		"x":   x,
		"y":   y,
		"kid": kid,
		"use": "sig",
		"alg": "ES256",
	}
}

// base64URLEncode pads a big-endian byte slice to size bytes and returns base64url without padding.
func base64URLEncode(b []byte, size int) string {
	// Left-pad to the required size
	if len(b) < size {
		padded := make([]byte, size)
		copy(padded[size-len(b):], b)
		b = padded
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// computeJKT computes the JWK Thumbprint (RFC 7638) for an EC key.
func computeJKT(jwkDict map[string]interface{}) string {
	canonical := map[string]interface{}{
		"crv": jwkDict["crv"],
		"kty": jwkDict["kty"],
		"x":   jwkDict["x"],
		"y":   jwkDict["y"],
	}
	data, _ := json.Marshal(canonical)
	hash := sha256.Sum256(data)
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

// computeJKTFromStringMap is the same as computeJKT but for string-keyed maps.
func computeJKTFromStringMap(jwkDict map[string]string) string {
	canonical := map[string]string{
		"crv": jwkDict["crv"],
		"kty": jwkDict["kty"],
		"x":   jwkDict["x"],
		"y":   jwkDict["y"],
	}
	data, _ := json.Marshal(canonical)
	hash := sha256.Sum256(data)
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

// signJWT signs a JWT with the given claims using ES256.
func signJWT(claims map[string]interface{}, key *ecdsa.PrivateKey, kid string) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims(claims))
	token.Header["kid"] = kid
	return token.SignedString(key)
}

// parseJWTUnverified decodes a JWT without signature verification (for demo flexibility).
func parseJWTUnverified(tokenString string) (jwt.MapClaims, error) {
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	token, _, err := parser.ParseUnverified(tokenString, jwt.MapClaims{})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, fmt.Errorf("invalid claims type")
	}
	return claims, nil
}

// parseJWTHeaderUnverified extracts the header from a JWT without verification.
func parseJWTHeaderUnverified(tokenString string) (map[string]interface{}, error) {
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	token, _, err := parser.ParseUnverified(tokenString, jwt.MapClaims{})
	if err != nil {
		return nil, err
	}
	return token.Header, nil
}

// nowUnix returns the current Unix timestamp.
func nowUnix() int64 {
	return time.Now().Unix()
}

// bigIntFromBase64URL decodes a base64url string to a big.Int.
func bigIntFromBase64URL(s string) *big.Int {
	b, _ := base64.RawURLEncoding.DecodeString(s)
	return new(big.Int).SetBytes(b)
}
