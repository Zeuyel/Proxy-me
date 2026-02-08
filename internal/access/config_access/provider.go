package configaccess

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	sdkaccess "github.com/router-for-me/CLIProxyAPI/v6/sdk/access"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

var registerOnce sync.Once

// Register ensures the config-access provider is available to the access manager.
func Register() {
	registerOnce.Do(func() {
		sdkaccess.RegisterProvider(sdkconfig.AccessProviderTypeConfigAPIKey, newProvider)
	})
}

type provider struct {
	name      string
	keys      map[string]struct{}
	expiresAt map[string]time.Time
	now       func() time.Time
}

func newProvider(cfg *sdkconfig.AccessProvider, _ *sdkconfig.SDKConfig) (sdkaccess.Provider, error) {
	name := cfg.Name
	if name == "" {
		name = sdkconfig.DefaultAccessProviderName
	}
	keys := make(map[string]struct{}, len(cfg.APIKeys))
	for _, key := range cfg.APIKeys {
		if key == "" {
			continue
		}
		keys[key] = struct{}{}
	}
	expiresAt := parseExpiryMap(cfg)
	return &provider{name: name, keys: keys, expiresAt: expiresAt, now: time.Now}, nil
}

func parseExpiryMap(cfg *sdkconfig.AccessProvider) map[string]time.Time {
	if cfg == nil || len(cfg.Config) == 0 {
		return nil
	}
	raw, ok := cfg.Config["api-key-expiry"]
	if !ok || raw == nil {
		raw = cfg.Config["apiKeyExpiry"]
	}
	if raw == nil {
		return nil
	}

	out := map[string]time.Time{}

	switch v := raw.(type) {
	case map[string]string:
		for key, val := range v {
			key = strings.TrimSpace(key)
			val = strings.TrimSpace(val)
			if key == "" || val == "" {
				continue
			}
			if ts, err := time.Parse(time.RFC3339, val); err == nil {
				out[key] = ts
			}
		}
	case map[string]any:
		for key, valAny := range v {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			val := strings.TrimSpace(toString(valAny))
			if val == "" {
				continue
			}
			if ts, err := time.Parse(time.RFC3339, val); err == nil {
				out[key] = ts
			}
		}
	default:
		return nil
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

func toString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	default:
		return ""
	}
}

func (p *provider) Identifier() string {
	if p == nil || p.name == "" {
		return sdkconfig.DefaultAccessProviderName
	}
	return p.name
}

func (p *provider) Authenticate(_ context.Context, r *http.Request) (*sdkaccess.Result, error) {
	if p == nil {
		return nil, sdkaccess.ErrNotHandled
	}
	if len(p.keys) == 0 {
		return nil, sdkaccess.ErrNotHandled
	}
	authHeader := r.Header.Get("Authorization")
	authHeaderGoogle := r.Header.Get("X-Goog-Api-Key")
	authHeaderAnthropic := r.Header.Get("X-Api-Key")
	queryKey := ""
	queryAuthToken := ""
	if r.URL != nil {
		queryKey = r.URL.Query().Get("key")
		queryAuthToken = r.URL.Query().Get("auth_token")
	}
	if authHeader == "" && authHeaderGoogle == "" && authHeaderAnthropic == "" && queryKey == "" && queryAuthToken == "" {
		return nil, sdkaccess.ErrNoCredentials
	}

	apiKey := extractBearerToken(authHeader)

	candidates := []struct {
		value  string
		source string
	}{
		{apiKey, "authorization"},
		{authHeaderGoogle, "x-goog-api-key"},
		{authHeaderAnthropic, "x-api-key"},
		{queryKey, "query-key"},
		{queryAuthToken, "query-auth-token"},
	}

	for _, candidate := range candidates {
		if candidate.value == "" {
			continue
		}
		if _, ok := p.keys[candidate.value]; ok {
			if p.expiresAt != nil {
				if exp, has := p.expiresAt[candidate.value]; has {
					nowFn := p.now
					if nowFn == nil {
						nowFn = time.Now
					}
					if !exp.IsZero() && !exp.After(nowFn()) {
						return nil, sdkaccess.ErrExpiredCredential
					}
				}
			}
			return &sdkaccess.Result{
				Provider:  p.Identifier(),
				Principal: candidate.value,
				Metadata: map[string]string{
					"source": candidate.source,
				},
			}, nil
		}
	}

	return nil, sdkaccess.ErrInvalidCredential
}

func extractBearerToken(header string) string {
	if header == "" {
		return ""
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 {
		return header
	}
	if strings.ToLower(parts[0]) != "bearer" {
		return header
	}
	return strings.TrimSpace(parts[1])
}
