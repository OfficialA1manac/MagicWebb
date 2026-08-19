package chain

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"testing"

	ethereum "github.com/ethereum/go-ethereum"
)

// fakeCaller answers eth_call from a canned function so tests can drive every
// branch of SupportsInterface without a node.
type fakeCaller struct {
	fn    func(data []byte) ([]byte, error)
	calls [][]byte
}

func (f *fakeCaller) CallContract(_ context.Context, msg ethereum.CallMsg, _ *big.Int) ([]byte, error) {
	f.calls = append(f.calls, msg.Data)
	return f.fn(msg.Data)
}

func (f *fakeCaller) BlockNumber(context.Context) (uint64, error) { return 0, nil }

// boolWord encodes a solidity bool return value.
func boolWord(b bool) []byte {
	w := make([]byte, 32)
	if b {
		w[31] = 1
	}
	return w
}

// answersTrueFor replies true only for the given interface id.
func answersTrueFor(id [4]byte) func([]byte) ([]byte, error) {
	return func(data []byte) ([]byte, error) {
		return boolWord(bytes.Equal(data[4:8], id[:])), nil
	}
}

const coll = "0x832d74Cfbb4617B50C32cD110dfe16837A359B35"

func TestSupportsInterfaceEncodesBytes4LeftAligned(t *testing.T) {
	f := &fakeCaller{fn: func([]byte) ([]byte, error) { return boolWord(true), nil }}
	if _, err := SupportsInterface(context.Background(), f, coll, InterfaceERC721); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := f.calls[0]
	if len(got) != 36 {
		t.Fatalf("calldata = %d bytes, want 36", len(got))
	}
	if !bytes.Equal(got[:4], supportsInterfaceSelector) {
		t.Fatalf("selector = %x, want %x", got[:4], supportsInterfaceSelector)
	}
	// bytes4 lives in the HIGH bytes of the word; the low 28 must be zero.
	if !bytes.Equal(got[4:8], InterfaceERC721[:]) {
		t.Fatalf("interface id = %x, want %x", got[4:8], InterfaceERC721)
	}
	if !bytes.Equal(got[8:36], make([]byte, 28)) {
		t.Fatalf("padding = %x, want 28 zero bytes", got[8:36])
	}
}

func TestDetectStandard(t *testing.T) {
	transport := errors.New("dial tcp: connection refused")

	tests := []struct {
		name    string
		fn      func([]byte) ([]byte, error)
		want    string
		wantErr error
	}{
		{
			name: "erc721",
			fn:   answersTrueFor(InterfaceERC721),
			want: "erc721",
		},
		{
			name: "erc1155",
			fn:   answersTrueFor(InterfaceERC1155),
			want: "erc1155",
		},
		{
			name: "neither",
			fn:   func([]byte) ([]byte, error) { return boolWord(false), nil },
			want: "",
		},
		{
			name: "empty return is a definitive no",
			fn:   func([]byte) ([]byte, error) { return nil, nil },
			want: "",
		},
		{
			name: "short return is a definitive no",
			fn:   func([]byte) ([]byte, error) { return []byte{0x01}, nil },
			want: "",
		},
		{
			name: "revert is a definitive no",
			fn:   func([]byte) ([]byte, error) { return nil, errors.New("execution reverted") },
			want: "",
		},
		{
			name: "no contract code is a definitive no",
			fn:   func([]byte) ([]byte, error) { return nil, errors.New("no contract code at given address") },
			want: "",
		},
		{
			name:    "transport failure propagates",
			fn:      func([]byte) ([]byte, error) { return nil, transport },
			wantErr: transport,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DetectStandard(context.Background(), &fakeCaller{fn: tt.fn}, coll)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("DetectStandard = %q, want %q", got, tt.want)
			}
		})
	}
}

// A 1155 collection must not cost two calls' worth of confusion: 721 is probed
// first and only a false answer falls through to the 1155 probe.
func TestDetectStandardProbeOrder(t *testing.T) {
	f := &fakeCaller{fn: answersTrueFor(InterfaceERC1155)}
	if _, err := DetectStandard(context.Background(), f, coll); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(f.calls) != 2 {
		t.Fatalf("made %d calls, want 2", len(f.calls))
	}
	if !bytes.Equal(f.calls[0][4:8], InterfaceERC721[:]) {
		t.Fatalf("first probe = %x, want ERC-721", f.calls[0][4:8])
	}

	f = &fakeCaller{fn: answersTrueFor(InterfaceERC721)}
	if _, err := DetectStandard(context.Background(), f, coll); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(f.calls) != 1 {
		t.Fatalf("made %d calls after a 721 hit, want 1", len(f.calls))
	}
}

// name() and symbol() are optional in the metadata extension, and the two
// encodings below both occur on real chains. Getting the bytes32 case wrong
// yields a name padded with NULs, which then reaches templates and search.
func TestDecodeString(t *testing.T) {
	abiString := func(s string) []byte {
		out := make([]byte, 64)
		out[31] = 32 // offset
		out[63] = byte(len(s))
		return append(out, []byte(s)...)
	}
	fixed32 := func(s string) []byte {
		out := make([]byte, 32)
		copy(out, s)
		return out
	}

	tests := []struct {
		name string
		in   []byte
		want string
	}{
		{"abi dynamic string", abiString("Magic Webb Animi"), "Magic Webb Animi"},
		{"bytes32 legacy", fixed32("ANIMI"), "ANIMI"},
		{"empty return", nil, ""},
		{"short return", []byte{0x01}, ""},
		{"control chars stripped", abiString("Ani\nmi\x00"), "Animi"},
		{"surrounding space trimmed", abiString("  Animi  "), "Animi"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decodeString(tt.in); got != tt.want {
				t.Fatalf("decodeString = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCollectionInfo(t *testing.T) {
	abiString := func(s string) []byte {
		out := make([]byte, 64)
		out[31] = 32
		out[63] = byte(len(s))
		return append(out, []byte(s)...)
	}
	f := &fakeCaller{fn: func(data []byte) ([]byte, error) {
		switch {
		case bytes.Equal(data[:4], nameSelector):
			return abiString("Magic Webb Animi"), nil
		case bytes.Equal(data[:4], symbolSelector):
			return abiString("ANIMI"), nil
		}
		return nil, errors.New("execution reverted")
	}}
	name, symbol, err := CollectionInfo(context.Background(), f, coll)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "Magic Webb Animi" || symbol != "ANIMI" {
		t.Fatalf("got (%q, %q)", name, symbol)
	}
}

// A contract without the optional getters must yield empty strings, not an
// error — the caller then keeps whatever it already stored.
func TestCollectionInfoMissingGetters(t *testing.T) {
	f := &fakeCaller{fn: func([]byte) ([]byte, error) { return nil, errors.New("execution reverted") }}
	name, symbol, err := CollectionInfo(context.Background(), f, coll)
	if err != nil || name != "" || symbol != "" {
		t.Fatalf("got (%q, %q, %v), want empty with no error", name, symbol, err)
	}
}

// A transport failure must propagate so the sweeper retries instead of
// recording a blank name over a good one.
func TestCollectionInfoTransportErrorPropagates(t *testing.T) {
	boom := errors.New("dial tcp: connection refused")
	f := &fakeCaller{fn: func([]byte) ([]byte, error) { return nil, boom }}
	if _, _, err := CollectionInfo(context.Background(), f, coll); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
}
