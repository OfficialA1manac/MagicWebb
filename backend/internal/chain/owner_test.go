package chain

import "testing"

func TestSameAddr(t *testing.T) {
	if !SameAddr("0xABC", "0xabc") {
		t.Fatal("case insensitive match expected")
	}
}
