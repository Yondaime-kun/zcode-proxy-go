package auth

import (
	"os"
	"testing"
)

func TestEncryptDecrypt(t *testing.T) {
	orig := "my-secret-key-12345"
	encrypted, err := encrypt([]byte(orig))
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}

	decrypted, err := decrypt(encrypted)
	if err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}

	if string(decrypted) != orig {
		t.Errorf("got %s, want %s", string(decrypted), orig)
	}
}

func TestSaveAndLoadCredential(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "cred-test-*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	cred := &Credential{
		ApiKey:   "test-api-key",
		Secret:   "test-secret",
		Provider: "zai",
		Jwt:      "test-jwt-token",
	}

	if cred.CredentialString() != "test-api-key.test-secret" {
		t.Errorf("CredentialString mismatch: %s", cred.CredentialString())
	}
}
