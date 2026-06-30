package github

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/config"
	"github.com/maryzam/ai-crew-localdev/internal/identity"
	"github.com/maryzam/ai-crew-localdev/internal/securefile"
)

// Signer holds parsed RSA private keys for GitHub App JWT signing.
// PEM material is loaded into memory once at startup and the file
// contents are never re-read.
type Signer struct {
	keys map[string]*rsa.PrivateKey // app ID -> private key
}

// NewSigner loads PEM files for each agent identity and parses them
// into RSA private keys. Returns an error if any PEM file is missing,
// unreadable, or contains an invalid key.
func NewSigner(identities *identity.IdentitiesFile) (*Signer, error) {
	keys := make(map[string]*rsa.PrivateKey, len(identities.Agents))

	for name, agent := range identities.Agents {
		keyPath := config.ExpandHome(agent.AppKey)

		pemData, err := securefile.ReadOwnerOnly(keyPath, 1<<20)
		if err != nil {
			return nil, fmt.Errorf("signer: agent %q: read PEM %s: %w", name, keyPath, err)
		}

		block, _ := pem.Decode(pemData)
		if block == nil {
			return nil, fmt.Errorf("signer: agent %q: no PEM block found in %s", name, keyPath)
		}

		key, err := parseRSAPrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("signer: agent %q: parse key from %s: %w", name, keyPath, err)
		}

		keys[agent.AppID] = key

		// Zero the PEM data in memory.
		for i := range pemData {
			pemData[i] = 0
		}
	}

	return &Signer{keys: keys}, nil
}

// parseRSAPrivateKey tries PKCS1 first, then PKCS8 format.
func parseRSAPrivateKey(der []byte) (*rsa.PrivateKey, error) {
	if key, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("not a valid PKCS1 or PKCS8 RSA private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("PKCS8 key is not RSA")
	}
	return key, nil
}

// SignJWT creates a GitHub App JWT for the given app ID.
// The JWT is valid for 10 minutes with iat backdated 60 seconds.
func (s *Signer) SignJWT(appID string) (string, error) {
	key, ok := s.keys[appID]
	if !ok {
		return "", fmt.Errorf("signer: no key loaded for app ID %q", appID)
	}

	now := time.Now()
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	claims := map[string]interface{}{
		"iss": appID,
		"iat": now.Add(-60 * time.Second).Unix(),
		"exp": now.Add(10 * time.Minute).Unix(),
	}

	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("signer: marshal header: %w", err)
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("signer: marshal claims: %w", err)
	}

	headerB64 := base64.RawURLEncoding.EncodeToString(headerJSON)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := headerB64 + "." + claimsB64

	hash := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
	if err != nil {
		return "", fmt.Errorf("signer: sign: %w", err)
	}

	sigB64 := base64.RawURLEncoding.EncodeToString(sig)
	return signingInput + "." + sigB64, nil
}
