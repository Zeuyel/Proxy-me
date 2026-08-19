package management

import (
	"testing"

	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
)

func TestPriceFingerprintUsesCanonicalRates(t *testing.T) {
	first := coreusage.PriceSnapshot{Version: "1", InputPerMillionUSD: floatPtr(1), OutputPerMillionUSD: floatPtr(2)}
	second := first
	if got, want := priceFingerprint("model", first), priceFingerprint("model", second); got != want {
		t.Fatalf("same rates must have stable fingerprint: %q != %q", got, want)
	}
	second.OutputPerMillionUSD = floatPtr(3)
	if priceFingerprint("model", first) == priceFingerprint("model", second) {
		t.Fatal("different rates must have different fingerprints")
	}
}

func floatPtr(value float64) *float64 { return &value }
