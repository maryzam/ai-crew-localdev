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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
)

func generateTestPEM(t *testing.T, dir, name string) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	pemPath := filepath.Join(dir, name+".pem")
	pemData := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	if err := os.WriteFile(pemPath, pemData, 0600); err != nil {
		t.Fatalf("write PEM: %v", err)
	}
	return pemPath
}

func testIdentities(t *testing.T) (*identity.IdentitiesFile, string) {
	t.Helper()
	dir := t.TempDir()
	pemPath := generateTestPEM(t, dir, "test-agent")

	return &identity.IdentitiesFile{
		SchemaVersion: "ai-agent-identities/v2",
		Agents: map[string]identity.AgentIdentity{
			"test-agent": {
				AppID:      "12345",
				AppKey:     pemPath,
				GitName:    "test[bot]",
				GitEmail:   "test@bot",
				GithubHost: "github.com",
				Tool:       "test",
				Model:      "test",
			},
		},
	}, dir
}

func TestNewSigner(t *testing.T) {
	idents, _ := testIdentities(t)
	signer, err := NewSigner(idents)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	if _, ok := signer.keys["12345"]; !ok {
		t.Error("expected signer to have key for app ID 12345")
	}
	if _, ok := signer.keys["99999"]; ok {
		t.Error("expected signer to not have key for app ID 99999")
	}
}

func TestNewSignerMissingPEM(t *testing.T) {
	idents := &identity.IdentitiesFile{
		Agents: map[string]identity.AgentIdentity{
			"bad": {AppID: "1", AppKey: "/nonexistent.pem"},
		},
	}
	_, err := NewSigner(idents)
	if err == nil {
		t.Fatal("expected error for missing PEM file")
	}
}

func TestSignJWT(t *testing.T) {
	idents, _ := testIdentities(t)
	signer, err := NewSigner(idents)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}

	token, err := signer.SignJWT("12345")
	if err != nil {
		t.Fatalf("SignJWT: %v", err)
	}

	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWT parts, got %d", len(parts))
	}

	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	var header map[string]string
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		t.Fatalf("unmarshal header: %v", err)
	}
	if header["alg"] != "RS256" {
		t.Errorf("header alg = %q, want RS256", header["alg"])
	}
	if header["typ"] != "JWT" {
		t.Errorf("header typ = %q, want JWT", header["typ"])
	}

	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	if claims["iss"] != "12345" {
		t.Errorf("claims iss = %v, want 12345", claims["iss"])
	}
	if _, ok := claims["iat"]; !ok {
		t.Error("claims missing iat")
	}
	if _, ok := claims["exp"]; !ok {
		t.Error("claims missing exp")
	}

	verifiedClaims, err := verifyJWT(signer, token, "12345")
	if err != nil {
		t.Fatalf("VerifyJWT: %v", err)
	}
	if verifiedClaims["iss"] != "12345" {
		t.Errorf("verified iss = %v, want 12345", verifiedClaims["iss"])
	}
}

func TestSignJWTUnknownAppID(t *testing.T) {
	idents, _ := testIdentities(t)
	signer, _ := NewSigner(idents)

	_, err := signer.SignJWT("99999")
	if err == nil {
		t.Fatal("expected error for unknown app ID")
	}
}

func TestSignerPKCS8Key(t *testing.T) {
	dir := t.TempDir()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	pkcs8Bytes, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal PKCS8: %v", err)
	}

	pemPath := filepath.Join(dir, "pkcs8.pem")
	pemData := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: pkcs8Bytes,
	})
	if err := os.WriteFile(pemPath, pemData, 0600); err != nil {
		t.Fatalf("write PEM: %v", err)
	}

	idents := &identity.IdentitiesFile{
		Agents: map[string]identity.AgentIdentity{
			"pkcs8-agent": {
				AppID:      "67890",
				AppKey:     pemPath,
				GitName:    "test[bot]",
				GitEmail:   "test@bot",
				GithubHost: "github.com",
				Tool:       "test",
				Model:      "test",
			},
		},
	}

	signer, err := NewSigner(idents)
	if err != nil {
		t.Fatalf("NewSigner with PKCS8: %v", err)
	}

	token, err := signer.SignJWT("67890")
	if err != nil {
		t.Fatalf("SignJWT with PKCS8: %v", err)
	}

	if _, err := verifyJWT(signer, token, "67890"); err != nil {
		t.Fatalf("VerifyJWT with PKCS8: %v", err)
	}
}

func verifyJWT(signer *Signer, token, appID string) (map[string]interface{}, error) {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT")
	}
	key, ok := signer.keys[appID]
	if !ok {
		return nil, fmt.Errorf("missing key")
	}
	signingInput := parts[0] + "." + parts[1]
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, hash[:], signature); err != nil {
		return nil, err
	}
	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims map[string]interface{}
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		return nil, err
	}
	return claims, nil
}
