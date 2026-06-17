package media

import (
	"math/big"
	"testing"
)

func TestResolveURI_IPFS(t *testing.T) {
	got := ResolveURI("ipfs://QmTest123", "1")
	want := ipfsGateways[0] + "QmTest123"
	if got != want {
		t.Fatalf("ResolveURI ipfs = %q, want %q", got, want)
	}
}

func TestResolveURI_BareCID(t *testing.T) {
	cid := "QmYwAPJzv5CZsnA625s3Xf2nemtYgPp88kkX5h4N6y3F1"
	got := ResolveURI(cid, "1")
	if got != ipfsGateways[0]+cid {
		t.Fatalf("bare CID = %q, want gateway prefix", got)
	}
}

func TestResolveURI_ERC1155Placeholder(t *testing.T) {
	id := big.NewInt(42)
	padded := make([]byte, 32)
	id.FillBytes(padded)
	got := ResolveURI("ipfs://base/{id}.json", "42")
	if got == "" || got == "ipfs://base/{id}.json" {
		t.Fatalf("placeholder not replaced: %q", got)
	}
}

func TestProxyAllowed_BlocksPrivate(t *testing.T) {
	if ProxyAllowed("http://127.0.0.1/secret") {
		t.Fatal("should block localhost")
	}
	if ProxyAllowed("http://10.0.0.1/x") {
		t.Fatal("should block 10.x")
	}
	if !ProxyAllowed("https://ipfs.io/ipfs/QmTest") {
		t.Fatal("should allow public gateway")
	}
}

func TestResolveCandidates_MultipleGateways(t *testing.T) {
	cands := ResolveCandidates("ipfs://QmABC", "1")
	if len(cands) < 2 {
		t.Fatalf("expected multiple gateway candidates, got %d", len(cands))
	}
}
