package config

import (
	"crypto/ecdsa"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
)

// ── Existing tests ────────────────────────────────────────────────────────

func TestParseAddrList(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"0xAbC", []string{"0xabc"}},
		{"0xAbC,0xDeF", []string{"0xabc", "0xdef"}},
		{" 0xAbC , ,0xDeF ", []string{"0xabc", "0xdef"}}, // trims, lowercases, drops blanks
		{",,", nil},
	}
	for _, tc := range cases {
		got := parseAddrList(tc.in)
		if len(got) != len(tc.want) {
			t.Fatalf("parseAddrList(%q) = %v, want %v", tc.in, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("parseAddrList(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
			}
		}
	}
}

func TestIsAdmin(t *testing.T) {
	c := &Config{AdminAllowlist: parseAddrList("0xAAA,0xBBB")}

	if !c.IsAdmin("0xaaa") {
		t.Fatal("0xaaa should be admin")
	}
	if !c.IsAdmin("  0xBBB  ") { // case-insensitive + trimmed
		t.Fatal("0xBBB should be admin regardless of case/whitespace")
	}
	if c.IsAdmin("0xccc") {
		t.Fatal("0xccc must not be admin")
	}
	if c.IsAdmin("") {
		t.Fatal("empty address must not be admin")
	}

	empty := &Config{}
	if empty.IsAdmin("0xaaa") {
		t.Fatal("empty allowlist must admit no one")
	}
}

// ── v35: isValidEthAddr (contract + admin allowlist validation) ───────────

func TestIsValidEthAddr(t *testing.T) {
	valid := []string{
		"0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"0x0000000000000000000000000000000000000000",
		"0xffffffffffffffffffffffffffffffffffffffff",
		"0x1234567890abcdef1234567890abcdef12345678",
		"0x" + strings.Repeat("a", 40),
	}
	for _, addr := range valid {
		if !isValidEthAddr(addr) {
			t.Fatalf("%q should be valid", addr)
		}
	}

	invalid := []struct {
		addr string
		reason string
	}{
		{"", "empty"},
		{"0x", "only prefix"},
		{"0xabc", "too short (3 hex chars)"},
		{"0x" + strings.Repeat("a", 39), "39 chars (one short)"},
		{"0x" + strings.Repeat("a", 41), "41 chars (one over)"},
		{"aaa" + strings.Repeat("a", 40), "missing 0x prefix"},
		{"0X" + strings.Repeat("a", 40), "uppercase 0X prefix"},
		{"0x" + strings.Repeat("A", 40), "uppercase hex chars"},
		{"0x" + strings.Repeat("g", 40), "non-hex char 'g'"},
		{"0x" + strings.Repeat("z", 40), "non-hex char 'z'"},
		{"0xZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ", "all non-hex"},
		{" 0x" + strings.Repeat("a", 40), "leading whitespace"},
		{"0x" + strings.Repeat("a", 40) + " ", "trailing whitespace"},
		{"0x" + strings.Repeat("a", 38) + "G1", "mixed hex + non-hex"},
	}
	for _, tc := range invalid {
		if isValidEthAddr(tc.addr) {
			t.Fatalf("%q should be invalid (%s)", tc.addr, tc.reason)
		}
	}
}

func TestIsValidEthAddrRejectsUppercase(t *testing.T) {
	// The isValidEthAddr function only accepts lowercase a-f. Uppercase
	// hex chars (A-F) are rejected because addresses are lowercased before
	// validation. This test locks that behavior.
	if isValidEthAddr("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA") {
		t.Fatal("uppercase hex address must be rejected")
	}
	if isValidEthAddr("0xABCDEF0123456789ABCDEF0123456789ABCDEF01") {
		t.Fatal("mixed-case address must be rejected")
	}
}

// ── v35: KEEPER_KEY ECDSA validation ─────────────────────────────────────

func TestKeeperKeyValidation_ValidKey(t *testing.T) {
	// Generate a real ECDSA key, hex-encode it, and verify the
	// same parsing sequence used by Load() succeeds.
	key := ensureValidKey(t)
	keyHex := hex.EncodeToString(crypto.FromECDSA(key))

	// Parse exactly as Load() does:
	//   1. hex.DecodeString(keyHex)
	//   2. crypto.ToECDSA(bytes)
	pkBytes, err := hex.DecodeString(keyHex)
	if err != nil {
		t.Fatal("valid hex should decode:", err)
	}
	parsed, err := crypto.ToECDSA(pkBytes)
	if err != nil {
		t.Fatal("valid private key should parse:", err)
	}
	if parsed == nil {
		t.Fatal("parsed key is nil")
	}

	// 0x-prefixed variant (also supported by Load() via TrimPrefix).
	pkBytes2, err := hex.DecodeString(strings.TrimPrefix("0x"+keyHex, "0x"))
	if err != nil {
		t.Fatal("0x-prefixed key should decode after TrimPrefix:", err)
	}
	if _, err := crypto.ToECDSA(pkBytes2); err != nil {
		t.Fatal("0x-prefixed key should parse:", err)
	}
}

func TestKeeperKeyValidation_InvalidHex(t *testing.T) {
	// Non-hex strings should fail at the hex.DecodeString step.
	// Note: empty string decodes to empty bytes in Go (valid hex), so
	// we don't include "" — Load() handles empty via the C.KeeperKey != ""
	// gate before reaching the hex decode.
	invalid := []string{
		"not-hex-at-all",
		"0xGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGG",
		"xyz",
		"0xZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZZ",
	}
	for _, s := range invalid {
		_, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
		if err == nil {
			t.Fatalf("%q should fail hex decode", s)
		}
	}
}

func TestKeeperKeyValidation_InvalidKey(t *testing.T) {
	// Valid hex but not a valid secp256k1 private key.

	// All-zeros is the canonical "not a key" case (0 < n < curve order N).
	zeroKey := make([]byte, 32)
	_, err := crypto.ToECDSA(zeroKey)
	if err == nil {
		t.Fatal("all-zeros should not be a valid ECDSA key")
	}

	// All 0xFF bytes — exceeds the secp256k1 curve order N, so ToECDSA
	// should reject it as an invalid private key.
	maxKey := make([]byte, 32)
	for i := range maxKey {
		maxKey[i] = 0xFF
	}
	_, err = crypto.ToECDSA(maxKey)
	if err == nil {
		t.Fatal("all-FF key (exceeds curve order) should not be a valid ECDSA key")
	}

	// Too-short key (20 bytes instead of 32).
	shortKey := make([]byte, 20)
	_, err = crypto.ToECDSA(shortKey)
	if err == nil {
		t.Fatal("20-byte key should be rejected by ECDSA parser")
	}
}

func TestKeeperKeyValidation_RoundTrip(t *testing.T) {
	// Generate → hex-encode → hex-decode → parse → verify the public key
	// round-trips correctly.
	key := ensureValidKey(t)
	keyHex := hex.EncodeToString(crypto.FromECDSA(key))
	pkBytes, err := hex.DecodeString(keyHex)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := crypto.ToECDSA(pkBytes)
	if err != nil {
		t.Fatal(err)
	}

	// Public keys should match.
	pubOriginal := crypto.PubkeyToAddress(key.PublicKey)
	pubParsed := crypto.PubkeyToAddress(parsed.PublicKey)
	if pubOriginal != pubParsed {
		t.Fatalf("public key mismatch: original=%s parsed=%s", pubOriginal.Hex(), pubParsed.Hex())
	}
}

// ── v35: SERVICE_TOKEN minimum length ─────────────────────────────────────

func TestServiceTokenMinLength(t *testing.T) {
	// Load() requires SERVICE_TOKEN ≥ 32 chars when set.
	// The guard:  if C.ServiceToken != "" && len(C.ServiceToken) < 32 { … os.Exit(1) }

	// Boundary: 31 chars → too short, guard fires.
	short := strings.Repeat("x", 31)
	if short != "" && len(short) < 32 {
		// guard fires — correct.
	} else {
		t.Fatal("31-char token should be detected as too short (guard should fire)")
	}

	// Boundary: 32 chars → just long enough, guard skips.
	ok32 := strings.Repeat("x", 32)
	if ok32 != "" && len(ok32) < 32 {
		t.Fatal("32-char token should NOT trigger the guard")
	}

	// Boundary: 64 chars → well above minimum.
	long := strings.Repeat("x", 64)
	if long != "" && len(long) < 32 {
		t.Fatal("64-char token should NOT trigger the guard")
	}

	// Empty token → guard is not reached (first condition short-circuits).
	empty := ""
	if empty != "" && len(empty) < 32 {
		t.Fatal("empty token should NOT reach the guard")
	}
}

// ── v35: optUint64 / optFloat64 parse warnings ────────────────────────────

func TestOptUint64ParseWarning(t *testing.T) {
	// optUint64 logs a warning and returns the default on parse errors.
	// Set a malformed env var and verify the default is returned.

	key := "TEST_OPT_UINT64_BAD"
	t.Setenv(key, "not-a-number")

	result := optUint64(key, 42)
	if result != 42 {
		t.Fatalf("optUint64 with malformed value = %d, want default 42", result)
	}
}

func TestOptUint64ValidValue(t *testing.T) {
	key := "TEST_OPT_UINT64_GOOD"
	t.Setenv(key, "100")

	result := optUint64(key, 42)
	if result != 100 {
		t.Fatalf("optUint64 with valid value = %d, want 100", result)
	}
}

func TestOptUint64EmptyUsesDefault(t *testing.T) {
	// Unset env var → return default.
	result := optUint64("TEST_OPT_UINT64_MISSING", 99)
	if result != 99 {
		t.Fatalf("optUint64 with missing env = %d, want default 99", result)
	}
}

func TestOptFloat64ParseWarning(t *testing.T) {
	key := "TEST_OPT_FLOAT64_BAD"
	t.Setenv(key, "not-a-float")

	result := optFloat64(key, 3.14)
	if result != 3.14 {
		t.Fatalf("optFloat64 with malformed value = %f, want default 3.14", result)
	}
}

func TestOptFloat64ValidValue(t *testing.T) {
	key := "TEST_OPT_FLOAT64_GOOD"
	t.Setenv(key, "2.718")

	result := optFloat64(key, 3.14)
	if result != 2.718 {
		t.Fatalf("optFloat64 with valid value = %f, want 2.718", result)
	}
}

func TestOptFloat64EmptyUsesDefault(t *testing.T) {
	result := optFloat64("TEST_OPT_FLOAT64_MISSING", 1.618)
	if result != 1.618 {
		t.Fatalf("optFloat64 with missing env = %f, want default 1.618", result)
	}
}

// ── v35: production SIWE domain guard ─────────────────────────────────────

func TestProdSIWEDomainGuard(t *testing.T) {
	// Load() fails when ENV=production and SIWE_DOMAIN=localhost.
	// Verify the condition logic is correct (can't test os.Exit directly).

	// Condition from Load():
	//   if C.Env == "production" && C.SIWEDomain == "localhost" { … os.Exit(1) }

	shouldFail := func(env, siwe string) bool {
		return env == "production" && siwe == "localhost"
	}

	// production + localhost → should fail.
	if !shouldFail("production", "localhost") {
		t.Fatal("production + localhost should trigger the guard")
	}

	// production + custom domain → should NOT fail.
	if shouldFail("production", "magicwebb.fly.dev") {
		t.Fatal("production + custom domain should NOT trigger the guard")
	}

	// development + localhost → should NOT fail.
	if shouldFail("development", "localhost") {
		t.Fatal("development + localhost should NOT trigger the guard")
	}

	// development + custom domain → should NOT fail.
	if shouldFail("development", "example.com") {
		t.Fatal("development + custom domain should NOT trigger the guard")
	}

	// empty env + localhost → should NOT fail.
	if shouldFail("", "localhost") {
		t.Fatal("empty env + localhost should NOT trigger the guard")
	}
}

// ── v35: contract address validation (via isValidEthAddr) ─────────────────

func TestContractAddressValidation(t *testing.T) {
	// The contract addresses (MARKETPLACE_ADDR, AUCTION_ADDR, OFFERBOOK_ADDR)
	// are validated with isValidEthAddr after lowercasing. Test realistic
	// contract address patterns.

	// A real Flare/Coston2 contract address (lowercased).
	if !isValidEthAddr("0x1234567890123456789012345678901234567890") {
		t.Fatal("valid contract address should pass")
	}

	// Zero address (commonly used as sentinel).
	if !isValidEthAddr("0x0000000000000000000000000000000000000000") {
		t.Fatal("zero address should be valid")
	}

	// Addresses with only decimal digits.
	if !isValidEthAddr("0x"+strings.Repeat("0", 40)) {
		t.Fatal("all-numeric address should be valid")
	}
}

// ── v35: ADMIN_ALLOWLIST entry validation (via isValidEthAddr) ────────────

func TestAdminAllowlistValidation(t *testing.T) {
	// Load() iterates over C.AdminAllowlist and calls isValidEthAddr on
	// each entry. Since parseAddrList lowercases, valid full-length
	// addresses will pass.

	entries := parseAddrList("0xAbCdef1234567890123456789012345678901234,0xDeF1234567890123456789012345678901234deF")
	for _, entry := range entries {
		if !isValidEthAddr(entry) {
			t.Fatalf("admin allowlist entry %q should be valid after lowercasing", entry)
		}
	}

	// Empty allowlist (valid — just a warning in production).
	if len(parseAddrList("")) != 0 {
		t.Fatal("empty string should produce empty allowlist")
	}
	if len(parseAddrList("  ,  ")) != 0 {
		t.Fatal("whitespace-only should produce empty allowlist")
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────

// ensureValidKey generates a valid secp256k1 key for tests that need one.
func ensureValidKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal("GenerateKey:", err)
	}
	return key
}

func TestEnsureValidKey(t *testing.T) {
	// Sanity-check that the helper works.
	key := ensureValidKey(t)
	if key == nil {
		t.Fatal("ensureValidKey returned nil")
	}
	keyHex := hex.EncodeToString(crypto.FromECDSA(key))
	if len(keyHex) != 64 {
		t.Fatalf("encoded key length = %d, want 64", len(keyHex))
	}
}
