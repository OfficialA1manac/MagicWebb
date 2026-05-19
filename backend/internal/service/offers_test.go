package service

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEIP712NameHash(t *testing.T) {
	expected := crypto.Keccak256Hash([]byte("MagicWebbOfferBook"))
	assert.Equal(t, expected, nameHash,
		"EIP-712 domain name must be MagicWebbOfferBook — changing this breaks all in-flight signatures")
}

func TestVersionHash(t *testing.T) {
	expected := crypto.Keccak256Hash([]byte("1"))
	assert.Equal(t, expected, versionHash)
}

func TestDomainSeparatorDeterministic(t *testing.T) {
	addr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	ds1 := domainSeparator(114, addr)
	ds2 := domainSeparator(114, addr)
	assert.Equal(t, ds1, ds2, "domainSeparator must be deterministic")
	require.NotEqual(t, common.Hash{}, ds1, "domainSeparator must not be zero")
}

func TestDomainSeparatorChainIsolation(t *testing.T) {
	addr := common.HexToAddress("0xDeaDbeefdEAdbeefdEadbEEFdeadbeEFdEaDbeeF")
	ds114 := domainSeparator(114, addr) // Coston2
	ds1 := domainSeparator(1, addr)    // Mainnet
	assert.NotEqual(t, ds114, ds1, "domainSeparator must differ across chains")
}
