package imagestore

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeEvicter struct {
	cands   []EvictableBlob
	deleted []string
	failOn  string
}

func (f *fakeEvicter) ListEvictableBlobs(_ context.Context, limit int) ([]EvictableBlob, error) {
	if limit < len(f.cands) {
		return f.cands[:limit], nil
	}
	return f.cands, nil
}
func (f *fakeEvicter) DeleteBlob(_ context.Context, sha string) (int64, error) {
	if sha == f.failOn {
		return 0, errors.New("boom")
	}
	f.deleted = append(f.deleted, sha)
	for _, c := range f.cands {
		if c.Sha256 == sha {
			return c.Bytes, nil
		}
	}
	return 0, nil
}

func h(c byte) string { return strings.Repeat(string(c), 64) }

func TestEvictUnreferencedStopsAtTarget(t *testing.T) {
	f := &fakeEvicter{cands: []EvictableBlob{{h('a'), 100}, {h('b'), 200}, {h('c'), 300}}}
	freed, n, err := EvictUnreferenced(context.Background(), f, 250)
	if err != nil {
		t.Fatal(err)
	}
	if freed != 300 || n != 2 || len(f.deleted) != 2 || f.deleted[0] != h('a') {
		t.Fatalf("want LRU a,b freed=300 n=2; got freed=%d n=%d deleted=%v", freed, n, f.deleted)
	}
}

func TestEvictUnreferencedNoopWhenNothingWanted(t *testing.T) {
	f := &fakeEvicter{cands: []EvictableBlob{{h('a'), 100}}}
	freed, n, err := EvictUnreferenced(context.Background(), f, 0)
	if err != nil || freed != 0 || n != 0 || len(f.deleted) != 0 {
		t.Fatalf("want noop, got freed=%d n=%d err=%v", freed, n, err)
	}
}

func TestEvictUnreferencedSkipsMalformedHashAndReportsDeleteError(t *testing.T) {
	f := &fakeEvicter{cands: []EvictableBlob{{"not-a-hash", 100}, {h('b'), 200}, {h('c'), 300}}, failOn: h('c')}
	freed, n, err := EvictUnreferenced(context.Background(), f, 1000)
	if err == nil || freed != 200 || n != 1 {
		t.Fatalf("want b deleted then error on c; got freed=%d n=%d err=%v", freed, n, err)
	}
}
