package cache

import (
	"context"
	"errors"
	"testing"
)

func TestKey(t *testing.T) {
	cases := []struct {
		chain uint64
		parts []string
		want  string
	}{
		{114, []string{"ls", "0xabc||recent|||50|map[]"}, "mw:114:ls:0xabc||recent|||50|map[]"},
		{19, []string{"col", "0xabc"}, "mw:19:col:0xabc"},
		{14, []string{"tr", "24h", "20"}, "mw:14:tr:24h:20"},
		{114, nil, "mw:114"},
	}
	for _, c := range cases {
		if got := Key(c.chain, c.parts...); got != c.want {
			t.Errorf("Key(%d, %v) = %q, want %q", c.chain, c.parts, got, c.want)
		}
	}
	// Two networks never produce the same key for the same logical entry.
	if Key(114, "ls", "x") == Key(19, "ls", "x") {
		t.Fatal("keys for different chains collided")
	}
}

// fakeBinder is an in-memory ChainBinder (no miniredis in go.mod).
type fakeBinder struct {
	data   map[string]string
	setErr error
	getErr error
}

func (f *fakeBinder) SetNX(_ context.Context, key, value string) (bool, error) {
	if f.setErr != nil {
		return false, f.setErr
	}
	if _, ok := f.data[key]; ok {
		return false, nil
	}
	f.data[key] = value
	return true, nil
}

func (f *fakeBinder) Get(_ context.Context, key string) (string, error) {
	if f.getErr != nil {
		return "", f.getErr
	}
	return f.data[key], nil
}

func TestBindChain_FreshRedisClaimsMarker(t *testing.T) {
	fb := &fakeBinder{data: map[string]string{}}
	if err := BindChain(context.Background(), fb, 114); err != nil {
		t.Fatalf("fresh bind: %v", err)
	}
	if fb.data[bootChainKey] != "114" {
		t.Fatalf("marker = %q, want 114", fb.data[bootChainKey])
	}
	// Rebooting the same chain is idempotent.
	if err := BindChain(context.Background(), fb, 114); err != nil {
		t.Fatalf("same-chain rebind: %v", err)
	}
}

func TestBindChain_OtherChainIsRefused(t *testing.T) {
	fb := &fakeBinder{data: map[string]string{bootChainKey: "114"}}
	err := BindChain(context.Background(), fb, 19)
	if !errors.Is(err, ErrRedisChainMismatch) {
		t.Fatalf("err = %v, want ErrRedisChainMismatch", err)
	}
	for _, want := range []string{"bound to chain 114", "this app is chain 19", "never share Redis across networks"} {
		if !contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
	if fb.data[bootChainKey] != "114" {
		t.Fatal("refused bind must not overwrite the marker")
	}
}

func TestBindChain_TransportErrorsAreNotMismatches(t *testing.T) {
	fb := &fakeBinder{data: map[string]string{}, setErr: errors.New("dial tcp: refused")}
	err := BindChain(context.Background(), fb, 114)
	if err == nil || errors.Is(err, ErrRedisChainMismatch) {
		t.Fatalf("err = %v, want a plain transport error", err)
	}
}

func TestBindChainURL_EmptyIsNoop(t *testing.T) {
	if err := BindChainURL(context.Background(), "", 114); err != nil {
		t.Fatal(err)
	}
	// Unparsable URL: logged, not fatal (mirrors NewRedisOrMemory's fallback).
	if err := BindChainURL(context.Background(), "://nope", 114); err != nil {
		t.Fatal(err)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
