package auth

import (
	"strings"
	"sync"
	"time"
)

const proxyPoolCooldown = 30 * time.Minute

var proxyCooldownState = struct {
	mu         sync.Mutex
	bannedTill map[string]time.Time
}{
	bannedTill: make(map[string]time.Time),
}

// SplitProxyPool normalizes a proxy_url string into a deduplicated ordered list.
// It accepts comma or newline separated URLs.
func SplitProxyPool(raw string) []string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == ','
	})
	if len(fields) == 0 {
		return nil
	}

	out := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		proxyURL := strings.TrimSpace(field)
		if proxyURL == "" {
			continue
		}
		if _, ok := seen[proxyURL]; ok {
			continue
		}
		seen[proxyURL] = struct{}{}
		out = append(out, proxyURL)
	}
	return out
}

func HasProxyPoolEntries(raw string) bool {
	return len(SplitProxyPool(raw)) > 0
}

func FirstUsableProxyURL(raw string, now time.Time) string {
	for _, proxyURL := range SplitProxyPool(raw) {
		if !IsProxyURLCooling(proxyURL, now) {
			return proxyURL
		}
	}
	return ""
}

func BanProxyURL(proxyURL string, now time.Time) {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return
	}
	proxyCooldownState.mu.Lock()
	proxyCooldownState.bannedTill[proxyURL] = now.Add(proxyPoolCooldown)
	proxyCooldownState.mu.Unlock()
}

func ResetProxyURLCooldown(proxyURL string) {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return
	}
	proxyCooldownState.mu.Lock()
	delete(proxyCooldownState.bannedTill, proxyURL)
	proxyCooldownState.mu.Unlock()
}

func IsProxyURLCooling(proxyURL string, now time.Time) bool {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return false
	}
	proxyCooldownState.mu.Lock()
	defer proxyCooldownState.mu.Unlock()
	until, ok := proxyCooldownState.bannedTill[proxyURL]
	if !ok {
		return false
	}
	if !until.After(now) {
		delete(proxyCooldownState.bannedTill, proxyURL)
		return false
	}
	return true
}
