package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	EnvSecret   = "ZCODE_PROXY_CREDENTIAL_SECRET"
	EnvStoreDir = "ZCODE_PROXY_STORE_DIR"
)

func GetStorePath() string {
	if dir := strings.TrimSpace(os.Getenv(EnvStoreDir)); dir != "" {
		return filepath.Join(dir, "credentials.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".zcode-proxy", "credentials.json")
}

func nodePlatform() string {
	switch runtime.GOOS {
	case "windows":
		return "win32"
	case "darwin":
		return "darwin"
	default:
		return "linux"
	}
}

func nodeArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "arm64":
		return "arm64"
	default:
		return runtime.GOARCH
	}
}

func getEncryptionKey() []byte {
	if seed := os.Getenv(EnvSecret); seed != "" {
		h := sha256.Sum256([]byte(seed))
		return h[:]
	}
	home, _ := os.UserHomeDir()
	seed := fmt.Sprintf("%s-%s-%s", home, nodePlatform(), nodeArch())
	h := sha256.Sum256([]byte(seed))
	return h[:]
}

func encrypt(plaintext []byte) (string, error) {
	key := getEncryptionKey()
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func decrypt(b64Ciphertext string) ([]byte, error) {
	key := getEncryptionKey()
	data, err := base64.StdEncoding.DecodeString(b64Ciphertext)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	return gcm.Open(nil, nonce, ciphertext, nil)
}

type storeWrapper struct {
	Encrypted string `json:"encrypted"`
}

type MultiCredentialStore struct {
	Active   string                 `json:"active"`
	Accounts map[string]*Credential `json:"accounts"`
}

func LoadStore() (*MultiCredentialStore, error) {
	path := GetStorePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &MultiCredentialStore{
				Accounts: make(map[string]*Credential),
			}, nil
		}
		return nil, err
	}

	var wrapped storeWrapper
	if err := json.Unmarshal(data, &wrapped); err != nil {
		return nil, err
	}
	if wrapped.Encrypted == "" {
		return &MultiCredentialStore{
			Accounts: make(map[string]*Credential),
		}, nil
	}

	plain, err := decrypt(wrapped.Encrypted)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt credentials: %w", err)
	}

	// 1. Try parsing as MultiCredentialStore
	var multi MultiCredentialStore
	if err := json.Unmarshal(plain, &multi); err == nil && len(multi.Accounts) > 0 {
		if multi.Active == "" {
			for k := range multi.Accounts {
				multi.Active = k
				break
			}
		}
		return &multi, nil
	}

	// 2. Fallback: Parse as legacy single Credential and auto-migrate
	var cred Credential
	if err := json.Unmarshal(plain, &cred); err == nil && (cred.ApiKey != "" || cred.Jwt != "") {
		store := &MultiCredentialStore{
			Active:   "default",
			Accounts: map[string]*Credential{"default": &cred},
		}
		_ = SaveStore(store)
		return store, nil
	}

	return &MultiCredentialStore{
		Accounts: make(map[string]*Credential),
	}, nil
}

func SaveStore(store *MultiCredentialStore) error {
	path := GetStorePath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}

	raw, err := json.Marshal(store)
	if err != nil {
		return err
	}

	encrypted, err := encrypt(raw)
	if err != nil {
		return err
	}

	wrapped := storeWrapper{Encrypted: encrypted}
	out, err := json.MarshalIndent(wrapped, "", "  ")
	if err != nil {
		return err
	}

	tmp := fmt.Sprintf("%s.tmp-%d", path, os.Getpid())
	if err := os.WriteFile(tmp, out, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *MultiCredentialStore) Save() error {
	return SaveStore(s)
}

func SaveAccount(name string, cred *Credential) error {
	if name == "" {
		name = "default"
	}
	store, err := LoadStore()
	if err != nil {
		return err
	}
	if store.Accounts == nil {
		store.Accounts = make(map[string]*Credential)
	}
	store.Accounts[name] = cred
	if store.Active == "" || len(store.Accounts) == 1 {
		store.Active = name
	}
	return SaveStore(store)
}

func SaveCredential(cred *Credential) error {
	return SaveAccount("default", cred)
}

func LoadAccount(name string) (*Credential, error) {
	store, err := LoadStore()
	if err != nil {
		return nil, err
	}
	if store == nil || store.Accounts == nil {
		return nil, nil
	}
	return store.Accounts[name], nil
}

func LoadCredential() (*Credential, error) {
	store, err := LoadStore()
	if err != nil {
		return nil, err
	}
	if store == nil || len(store.Accounts) == 0 {
		return nil, nil
	}
	if cred, ok := store.Accounts[store.Active]; ok {
		return cred, nil
	}
	for _, cred := range store.Accounts {
		return cred, nil
	}
	return nil, nil
}

func RemoveAccount(name string) error {
	store, err := LoadStore()
	if err != nil {
		return err
	}
	if store.Accounts == nil {
		return nil
	}
	delete(store.Accounts, name)
	if store.Active == name {
		store.Active = ""
		for k := range store.Accounts {
			store.Active = k
			break
		}
	}
	return SaveStore(store)
}

func SetActiveAccount(name string) error {
	store, err := LoadStore()
	if err != nil {
		return err
	}
	if _, ok := store.Accounts[name]; !ok {
		return fmt.Errorf("account %q not found", name)
	}
	store.Active = name
	return SaveStore(store)
}

func ClearCredential() error {
	path := GetStorePath()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func ImportFromZCodeConfig(provider string) (*Credential, error) {
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".zcode", "v2", "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}

	var root struct {
		Provider map[string]struct {
			Options struct {
				ApiKey string `json:"apiKey"`
			} `json:"options"`
		} `json:"provider"`
	}

	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("invalid json in %s: %w", path, err)
	}

	codingPlanKey := fmt.Sprintf("builtin:%s-coding-plan", provider)
	entry, ok := root.Provider[codingPlanKey]
	apiKey := strings.TrimSpace(entry.Options.ApiKey)
	if !ok || apiKey == "" {
		// Try without -coding-plan
		fallbackKey := fmt.Sprintf("builtin:%s", provider)
		if fb, ok := root.Provider[fallbackKey]; ok {
			apiKey = strings.TrimSpace(fb.Options.ApiKey)
		}
	}
	if apiKey == "" {
		return nil, fmt.Errorf("no API key for %s in %s", codingPlanKey, path)
	}

	var secret string
	if parts := strings.SplitN(apiKey, ".", 2); len(parts) == 2 {
		apiKey = parts[0]
		secret = parts[1]
	}

	startPlanKey := fmt.Sprintf("builtin:%s-start-plan", provider)
	var jwt string
	if sp, ok := root.Provider[startPlanKey]; ok {
		jwt = strings.TrimSpace(sp.Options.ApiKey)
	}

	return &Credential{
		ApiKey:   apiKey,
		Secret:   secret,
		Provider: provider,
		Jwt:      jwt,
	}, nil
}
