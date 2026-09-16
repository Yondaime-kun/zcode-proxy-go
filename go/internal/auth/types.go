package auth

type Credential struct {
	ApiKey    string `json:"apiKey"`
	Secret    string `json:"secret,omitempty"`
	Provider  string `json:"provider"`
	ExpiresAt int64  `json:"expiresAt,omitempty"`
	UserId    string `json:"userId,omitempty"`
	Jwt       string `json:"jwt,omitempty"`
}

func (c *Credential) CredentialString() string {
	if c.Secret != "" {
		return c.ApiKey + "." + c.Secret
	}
	return c.ApiKey
}
