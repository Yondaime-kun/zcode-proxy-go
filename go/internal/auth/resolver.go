package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	zaiApiKeyName         = "zcode-api-key"
	defaultOrgMarker      = "\u9ED8\u8BA4\u673A\u6784" // 默认机构
	defaultProjectMarker  = "\u9ED8\u8BA4\u9879\u76EE" // 默认项目
)

type KeyResolver struct {
	client *http.Client
}

func NewKeyResolver() *KeyResolver {
	return &KeyResolver{
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (r *KeyResolver) requestBizApi(url, authHeader string, method string, bodyData interface{}) (map[string]interface{}, error) {
	var bodyReader io.Reader
	if bodyData != nil {
		b, err := json.Marshal(bodyData)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("biz API %s failed: %d: %s", url, resp.StatusCode, string(bodyBytes))
	}

	var root map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &root); err != nil {
		return nil, err
	}

	code, hasCode := root["code"]
	if hasCode && code != nil {
		switch c := code.(type) {
		case float64:
			if c != 0 && c != 200 {
				return nil, fmt.Errorf("biz API error code %v: %v", c, root["msg"])
			}
		case string:
			if c != "0" && c != "200" {
				return nil, fmt.Errorf("biz API error code %s: %v", c, root["msg"])
			}
		}
	}

	if data, ok := root["data"].(map[string]interface{}); ok {
		return data, nil
	}
	return root, nil
}

func (r *KeyResolver) ResolveZaiBizToken(accessToken string) (string, error) {
	payload, _ := json.Marshal(map[string]string{"token": accessToken})
	req, err := http.NewRequest("POST", "https://api.z.ai/api/auth/z/login", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("z/login failed with status %d", resp.StatusCode)
	}

	var res struct {
		AccessToken string `json:"access_token"`
		Data        struct {
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}

	token := res.AccessToken
	if token == "" {
		token = res.Data.AccessToken
	}
	if token == "" {
		return "", fmt.Errorf("z/login returned empty access token")
	}
	return token, nil
}

func (r *KeyResolver) ResolveCustomerInfo(host, authHeader string) (orgID, projectID string, err error) {
	url := fmt.Sprintf("%s/api/biz/customer/getCustomerInfo", host)
	data, err := r.requestBizApi(url, authHeader, "GET", nil)
	if err != nil {
		return "", "", err
	}

	orgs, _ := data["organizations"].([]interface{})
	if len(orgs) == 0 {
		orgs, _ = data["orgs"].([]interface{})
	}
	if len(orgs) == 0 {
		return "", "", fmt.Errorf("no organizations found")
	}

	var targetOrg map[string]interface{}
	for _, o := range orgs {
		if om, ok := o.(map[string]interface{}); ok {
			name, _ := om["organizationName"].(string)
			if name == "" {
				name, _ = om["name"].(string)
			}
			if strings.Contains(name, defaultOrgMarker) {
				targetOrg = om
				break
			}
		}
	}
	if targetOrg == nil {
		targetOrg, _ = orgs[0].(map[string]interface{})
	}

	orgID, _ = targetOrg["organizationId"].(string)
	if orgID == "" {
		orgID, _ = targetOrg["id"].(string)
	}

	projects, _ := targetOrg["projects"].([]interface{})
	if len(projects) == 0 {
		return "", "", fmt.Errorf("no projects found in organization")
	}

	var targetProj map[string]interface{}
	for _, p := range projects {
		if pm, ok := p.(map[string]interface{}); ok {
			pName, _ := pm["projectName"].(string)
			if pName == "" {
				pName, _ = pm["name"].(string)
			}
			if strings.Contains(pName, defaultProjectMarker) {
				targetProj = pm
				break
			}
		}
	}
	if targetProj == nil {
		targetProj, _ = projects[0].(map[string]interface{})
	}

	projectID, _ = targetProj["projectId"].(string)
	if projectID == "" {
		projectID, _ = targetProj["id"].(string)
	}

	return orgID, projectID, nil
}

func extractKeyString(m map[string]interface{}) string {
	candidates := []string{"apiKey", "api_key", "key", "token", "apiKeyId", "id"}
	for _, c := range candidates {
		if v, ok := m[c].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func findNamedApiKey(v interface{}, targetName string) string {
	switch val := v.(type) {
	case []interface{}:
		for _, item := range val {
			if k := findNamedApiKey(item, targetName); k != "" {
				return k
			}
		}
	case map[string]interface{}:
		name, _ := val["name"].(string)
		if name == targetName {
			if k := extractKeyString(val); k != "" {
				return k
			}
		}
		for _, sub := range val {
			if k := findNamedApiKey(sub, targetName); k != "" {
				return k
			}
		}
	}
	return ""
}

func findAnyApiKey(v interface{}) string {
	switch val := v.(type) {
	case []interface{}:
		for _, item := range val {
			if k := findAnyApiKey(item); k != "" {
				return k
			}
		}
	case map[string]interface{}:
		if k := extractKeyString(val); k != "" {
			return k
		}
		for _, sub := range val {
			if k := findAnyApiKey(sub); k != "" {
				return k
			}
		}
	}
	return ""
}

func (r *KeyResolver) FindOrCreateApiKey(host, authHeader, orgID, projectID string) (string, error) {
	listUrl := fmt.Sprintf("%s/api/biz/v1/organization/%s/projects/%s/api_keys", host, orgID, projectID)
	req, _ := http.NewRequest("GET", listUrl, nil)
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("Content-Type", "application/json")

	if resp, err := r.client.Do(req); err == nil {
		defer resp.Body.Close()
		bodyBytes, _ := io.ReadAll(resp.Body)
		var rawRoot interface{}
		if json.Unmarshal(bodyBytes, &rawRoot) == nil {
			if k := findNamedApiKey(rawRoot, zaiApiKeyName); k != "" {
				return k, nil
			}
			if k := findAnyApiKey(rawRoot); k != "" {
				return k, nil
			}
		}
	}

	// Create new API key
	createData := map[string]string{"name": zaiApiKeyName}
	data, err := r.requestBizApi(listUrl, authHeader, "POST", createData)
	if err != nil {
		// If duplicate, try fetching list again with query
		if strings.Contains(err.Error(), "duplicate") {
			// Try re-fetching without error
			req2, _ := http.NewRequest("GET", listUrl, nil)
			req2.Header.Set("Authorization", authHeader)
			if resp2, err2 := r.client.Do(req2); err2 == nil {
				defer resp2.Body.Close()
				bodyBytes, _ := io.ReadAll(resp2.Body)
				var rawRoot interface{}
				if json.Unmarshal(bodyBytes, &rawRoot) == nil {
					if k := findNamedApiKey(rawRoot, zaiApiKeyName); k != "" {
						return k, nil
					}
					if k := findAnyApiKey(rawRoot); k != "" {
						return k, nil
					}
				}
			}
		}
		return "", err
	}

	key := extractKeyString(data)
	if key == "" {
		return "", fmt.Errorf("failed to extract apiKey after creation")
	}
	return key, nil
}

func (r *KeyResolver) CopyApiKeySecret(host, authHeader, orgID, projectID, apiKey string) (string, string, error) {
	u := fmt.Sprintf("%s/api/biz/v1/organization/%s/projects/%s/api_keys/copy/%s", host, orgID, projectID, url.PathEscape(apiKey))
	data, err := r.requestBizApi(u, authHeader, "GET", nil)
	if err != nil {
		return apiKey, "", nil // fallback to bare apiKey
	}

	sec, _ := data["secretKey"].(string)
	if sec == "" {
		sec, _ = data["secret_key"].(string)
	}
	if sec == "" {
		sec, _ = data["secret"].(string)
	}
	return apiKey, sec, nil
}

func (r *KeyResolver) ResolveCodingPlanCredential(accessToken, provider, userID string) (*Credential, error) {
	var host string
	var authHeader string

	if provider == "zai" {
		bizToken, err := r.ResolveZaiBizToken(accessToken)
		if err != nil {
			return nil, err
		}
		host = "https://api.z.ai"
		authHeader = "Bearer " + bizToken
	} else {
		host = "https://open.bigmodel.cn"
		authHeader = "Bearer " + accessToken
	}

	orgID, projectID, err := r.ResolveCustomerInfo(host, authHeader)
	if err != nil {
		return nil, err
	}

	keyID, err := r.FindOrCreateApiKey(host, authHeader, orgID, projectID)
	if err != nil {
		return nil, err
	}

	apiKey, secret, err := r.CopyApiKeySecret(host, authHeader, orgID, projectID, keyID)
	if err != nil {
		return nil, err
	}

	return &Credential{
		ApiKey:   apiKey,
		Secret:   secret,
		Provider: provider,
		UserId:   userID,
	}, nil
}
