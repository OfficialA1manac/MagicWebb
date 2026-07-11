package main

import (
	"crypto/ecdsa"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/gofiber/fiber/v2"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
)

func signEIP191(t *testing.T, key *ecdsa.PrivateKey, message string) string {
	t.Helper()
	prefixed := fmt.Sprintf("\x19Ethereum Signed Message:\n%d%s", len(message), message)
	hash := crypto.Keccak256Hash([]byte(prefixed))
	sig, err := crypto.Sign(hash.Bytes(), key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return hexutil.Encode(sig)
}

func TestVerifyEIP191(t *testing.T) {
	key, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(key.PublicKey).Hex()
	msg := "magicwebb.test wants you to sign in\nNonce: abcd1234"
	sig := signEIP191(t, key, msg)

	ok, err := verifyEIP191(msg, sig, addr)
	if err != nil || !ok {
		t.Fatalf("valid signature must verify: ok=%v err=%v", ok, err)
	}

	// A signature must not verify against a different address.
	other, _ := crypto.GenerateKey()
	if ok, _ := verifyEIP191(msg, sig, crypto.PubkeyToAddress(other.PublicKey).Hex()); ok {
		t.Fatal("signature verified for the wrong address")
	}

	// A signature must not verify against a tampered message.
	if ok, _ := verifyEIP191(msg+"tampered", sig, addr); ok {
		t.Fatal("signature verified for a tampered message")
	}

	// Malformed signatures must error, not panic.
	for _, bad := range []string{"0xdeadbeef", "not-hex", ""} {
		if _, err := verifyEIP191(msg, bad, addr); err == nil {
			t.Fatalf("malformed signature %q should error", bad)
		}
	}
}

func TestSendHTMLWithConfigUsesLoadedRuntimeConfig(t *testing.T) {
	oldConfig := config.C
	t.Cleanup(func() {
		config.C = oldConfig
		htmlCache.Range(func(key, _ any) bool {
			htmlCache.Delete(key)
			return true
		})
	})

	config.C = config.Config{
		RPCURL:          "https://coston2-api.flare.network/ext/C/rpc",
		ChainID:         114,
		NetworkName:     "Flare Coston2",
		NativeCurrency:  "C2FLR",
		ExplorerURL:     "https://coston2-explorer.flare.network",
		WCProjectID:     "wc_public_id",
		MarketplaceAddr: "0x1111111111111111111111111111111111111111",
		AuctionAddr:     "0x2222222222222222222222222222222222222222",
		OfferBookAddr:   "0x3333333333333333333333333333333333333333",
	}

	dir := t.TempDir()
	htmlPath := filepath.Join(dir, "index.html")
	if err := os.WriteFile(htmlPath, []byte(`<!doctype html><html><head><title>x</title></head><body><span class="mw-cur">C2FLR</span></body></html>`), 0o600); err != nil {
		t.Fatalf("write temp html: %v", err)
	}

	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/", func(c *fiber.Ctx) error { return sendHTMLWithConfig(c, htmlPath) })

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)
	body := string(bodyBytes)

	for _, want := range []string{
		"window.MW_CHAIN_ID='114'",
		"window.MW_RPC_URL='https://coston2-api.flare.network/ext/C/rpc'",
		"window.MW_NETWORK_NAME='Flare Coston2'",
		"window.MW_NATIVE_CURRENCY='C2FLR'",
		"window.MW_MARKETPLACE='0x1111111111111111111111111111111111111111'",
		"window.MW_AUCTION='0x2222222222222222222222222222222222222222'",
		"window.MW_OFFERBOOK='0x3333333333333333333333333333333333333333'",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("response missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "window.MW_CHAIN_ID='0'") {
		t.Fatalf("response used package-init zero config:\n%s", body)
	}
}
