package main

import (
	"crypto/ecdsa"
	"fmt"
	"testing"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
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
