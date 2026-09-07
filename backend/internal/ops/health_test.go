package ops

import (
	"math/big"
	"testing"
)

func TestKeeperLevel(t *testing.T) {
	min := big.NewInt(100)
	cases := []struct {
		bal  *big.Int
		min  *big.Int
		want string
	}{
		{big.NewInt(100), min, LevelOK},
		{big.NewInt(1000), min, LevelOK},
		{big.NewInt(99), min, LevelWarning},
		{big.NewInt(20), min, LevelWarning},  // exactly 20% is still a warning
		{big.NewInt(19), min, LevelCritical}, // below 20%
		{big.NewInt(0), min, LevelCritical},
		{nil, min, LevelUnknown},
		{big.NewInt(5), nil, LevelDisabled},
		{big.NewInt(5), big.NewInt(0), LevelDisabled},
	}
	for _, c := range cases {
		if got := KeeperLevel(c.bal, c.min); got != c.want {
			t.Errorf("KeeperLevel(%v, %v) = %s, want %s", c.bal, c.min, got, c.want)
		}
	}
}

func TestSnapshotsRoundTrip(t *testing.T) {
	ResetForTest()
	if Keeper().Level != LevelDisabled {
		t.Fatal("fresh keeper level must be disabled")
	}
	SetKeeper(KeeperHealth{Address: "0xabc", Level: LevelWarning})
	IncUnderpriced()
	IncUnderpriced()
	if k := Keeper(); k.Address != "0xabc" || k.Level != LevelWarning || k.UnderpricedTotal != 2 {
		t.Fatalf("keeper snapshot: %+v", k)
	}
	SetImageStore(ImageStoreHealth{Bytes: 10, CapBytes: 100})
	AddEvicted(3)
	if h := ImageStore(); h.Bytes != 10 || h.CapBytes != 100 || h.Evicted != 3 {
		t.Fatalf("image store snapshot: %+v", h)
	}
	SetLag(LagHealth{Blocks: 42, Alerting: true, Since: 7})
	if l := Lag(); l.Blocks != 42 || !l.Alerting || l.Since != 7 {
		t.Fatalf("lag snapshot: %+v", l)
	}
	ResetForTest()
	if Underpriced() != 0 || EvictedTotal() != 0 {
		t.Fatal("reset must zero the counters")
	}
}
