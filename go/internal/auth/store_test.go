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

func TestStoreDirOverride(t *testing.T) {
	tempDir := t.TempDir()
	t.Setenv(EnvStoreDir, tempDir)

	expected := tempDir + "/credentials.json"
	if got := GetStorePath(); got != expected {
		t.Errorf("GetStorePath() = %q, want %q", got, expected)
	}

	cred := &Credential{
		ApiKey:   "override-key",
		Secret:   "override-secret",
		Provider: "zai",
	}

	if err := SaveCredential(cred); err != nil {
		t.Fatalf("SaveCredential failed: %v", err)
	}

	loaded, err := LoadCredential()
	if err != nil {
		t.Fatalf("LoadCredential failed: %v", err)
	}
	if loaded == nil || loaded.ApiKey != "override-key" {
		t.Fatalf("unexpected loaded credential: %+v", loaded)
	}
}
