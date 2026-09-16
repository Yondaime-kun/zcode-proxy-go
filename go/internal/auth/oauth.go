package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func saveAuthUrl(url string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dir := filepath.Join(home, ".zcode-proxy")
	_ = os.MkdirAll(dir, 0700)
	_ = os.WriteFile(filepath.Join(dir, "auth_url.txt"), []byte(url+"\n"), 0600)
}

type OAuthResult struct {
	AccessToken string
	Provider    string
	UserId      string
	Jwt         string
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func openBrowser(url string) {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	case "darwin":
		cmd = "open"
		args = []string{url}
	default:
		cmd = "xdg-open"
		args = []string{url}
	}
	_ = exec.Command(cmd, args...).Start()
}

// ZaiOAuth implements server-mediated polling login
func LoginZai(ctx context.Context) (*OAuthResult, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	pollToken := randomHex(32)

	initPayload, _ := json.Marshal(map[string]string{"provider": "zai"})
	req, err := http.NewRequestWithContext(ctx, "POST", "https://zcode.z.ai/api/v1/oauth/cli/init", bytes.NewReader(initPayload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+pollToken)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to init zai oauth: %w", err)
	}
	defer resp.Body.Close()

	var initResp struct {
		Code int `json:"code"`
		Data struct {
			FlowId          string `json:"flow_id"`
			AuthorizeUrl    string `json:"authorize_url"`
			ExpiresAt       int64  `json:"expires_at"`
			PollIntervalSec int    `json:"poll_interval_sec"`
		} `json:"data"`
		Msg string `json:"msg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&initResp); err != nil {
		return nil, err
	}
	if initResp.Code != 0 || initResp.Data.FlowId == "" {
		return nil, fmt.Errorf("init failed: %s", initResp.Msg)
	}

	authUrl := initResp.Data.AuthorizeUrl
	saveAuthUrl(authUrl)
	log.Printf("[auth] open URL in browser to authorize:")
	log.Printf("%s", authUrl)
	fmt.Println("Please open this URL in your browser to authorize:")
	fmt.Printf("\n  %s\n\n", authUrl)
	fmt.Println("Waiting for authorization... (expires in 300s)")
	openBrowser(authUrl)

	pollUrl := fmt.Sprintf("https://zcode.z.ai/api/v1/oauth/cli/poll/%s", initResp.Data.FlowId)
	pollInterval := 2 * time.Second
	if initResp.Data.PollIntervalSec > 0 {
		pollInterval = time.Duration(initResp.Data.PollIntervalSec) * time.Second
	}
	deadline := time.Now().Add(300 * time.Second)
	if initResp.Data.ExpiresAt > 0 {
		expTime := time.Unix(initResp.Data.ExpiresAt, 0)
		if expTime.Before(deadline) {
			deadline = expTime
		}
	}

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollInterval):
		}

		pollReq, _ := http.NewRequestWithContext(ctx, "GET", pollUrl, nil)
		pollReq.Header.Set("Authorization", "Bearer "+pollToken)
		pollResp, err := client.Do(pollReq)
		if err != nil {
			continue
		}

		var pollResult struct {
			Code int `json:"code"`
			Data struct {
				Status string `json:"status"`
				Token  string `json:"token"`
				User   struct {
					UserId string `json:"user_id"`
				} `json:"user"`
				Zai struct {
					AccessToken string `json:"access_token"`
				} `json:"zai"`
			} `json:"data"`
			Msg string `json:"msg"`
		}
		_ = json.NewDecoder(pollResp.Body).Decode(&pollResult)
		pollResp.Body.Close()

		if pollResult.Data.Status == "ready" {
			accessToken := strings.TrimSpace(pollResult.Data.Zai.AccessToken)
			if accessToken == "" {
				return nil, fmt.Errorf("zai login poll response missing access_token")
			}
			return &OAuthResult{
				AccessToken: accessToken,
				Provider:    "zai",
				UserId:      pollResult.Data.User.UserId,
				Jwt:         strings.TrimSpace(pollResult.Data.Token),
			}, nil
		}
		if pollResult.Data.Status == "failed" {
			return nil, fmt.Errorf("authorization failed on server")
		}
	}

	return nil, fmt.Errorf("login timed out")
}

// BigmodelOAuth implements auth-code flow with local loopback listener
func LoginBigmodel(ctx context.Context) (*OAuthResult, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to bind local callback port: %w", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	callbackUrl := fmt.Sprintf("http://127.0.0.1:%d/callback", port)
	state := randomHex(16)

	authUrl := fmt.Sprintf("https://bigmodel.cn/login?appId=zcode&redirect=%s&state=%s", callbackUrl, state)

	codeChan := make(chan string, 1)
	errChan := make(chan error, 1)

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/callback" {
				http.NotFound(w, r)
				return
			}
			returnedState := r.URL.Query().Get("state")
			if returnedState != state {
				http.Error(w, "State mismatch", http.StatusBadRequest)
				errChan <- fmt.Errorf("CSRF state mismatch")
				return
			}
			code := r.URL.Query().Get("code")
			if code == "" {
				http.Error(w, "Missing authorization code", http.StatusBadRequest)
				errChan <- fmt.Errorf("missing code in callback")
				return
			}

			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte("<html><body><h1>Login successful!</h1><p>You can close this window now.</p></body></html>"))
			codeChan <- code
		}),
	}

	go func() {
		_ = server.Serve(listener)
	}()
	defer server.Shutdown(context.Background())

	saveAuthUrl(authUrl)
	log.Printf("[auth] open URL in browser to authorize:")
	log.Printf("%s", authUrl)
	fmt.Println("Please open this URL in your browser to authorize:")
	fmt.Printf("\n  %s\n\n", authUrl)
	fmt.Println("Waiting for authorization... (expires in 300s)")
	openBrowser(authUrl)

	var code string
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-errChan:
		return nil, err
	case code = <-codeChan:
	case <-time.After(300 * time.Second):
		return nil, fmt.Errorf("login timed out")
	}

	// Exchange code for token
	client := &http.Client{Timeout: 15 * time.Second}
	exchangePayload, _ := json.Marshal(map[string]string{
		"code":         code,
		"redirect_uri": callbackUrl,
		"state":        state,
	})

	req, err := http.NewRequestWithContext(ctx, "POST", "https://zcode.z.ai/api/v1/oauth/token", bytes.NewReader(exchangePayload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange request failed: %w", err)
	}
	defer resp.Body.Close()

	var tokenResp struct {
		Code int `json:"code"`
		Data struct {
			AccessToken string `json:"access_token"`
			UserId      string `json:"user_id"`
			Jwt         string `json:"jwt"`
		} `json:"data"`
		Msg string `json:"msg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, err
	}
	if tokenResp.Code != 0 || tokenResp.Data.AccessToken == "" {
		return nil, fmt.Errorf("token exchange failed: %s", tokenResp.Msg)
	}

	return &OAuthResult{
		AccessToken: tokenResp.Data.AccessToken,
		Provider:    "bigmodel",
		UserId:      tokenResp.Data.UserId,
		Jwt:         tokenResp.Data.Jwt,
	}, nil
}
