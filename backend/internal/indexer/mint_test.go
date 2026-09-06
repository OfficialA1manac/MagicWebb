package indexer

import (
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
)

// A Transfer from the zero address is a mint: after the ownership write the
// handler records `to` as the token's minter (migration 044). A normal
// transfer (non-zero from) must not touch nft_tokens.minter.
func TestOnTransfer721RecordsMinter(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	h := &handlers{q: db.New(mock), bcast: sse.New()}
	coll := common.HexToAddress("0x642EB8dCa3e9e1eEc4302f0c51278c4dDA44207e")
	minter := "0x00000000000000000000000000000000000000c1"

	// ApplyTransfer721 runs in a transaction: DELETE + INSERT ownership.
	mock.ExpectBegin()
	mock.ExpectExec(`DELETE FROM nft_ownership`).WithArgs("0x642eb8dca3e9e1eec4302f0c51278c4dda44207e", "5").WillReturnResult(pgxmock.NewResult("DELETE", 0))
	mock.ExpectExec(`INSERT INTO nft_ownership`).WithArgs("0x642eb8dca3e9e1eec4302f0c51278c4dda44207e", "5", minter).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec(`INSERT INTO nft_tokens\(collection, token_id, owner\)`).WithArgs("0x642eb8dca3e9e1eec4302f0c51278c4dda44207e", "5", minter).WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectExec(`UPDATE listings SET orphaned=true`).WithArgs("0x642eb8dca3e9e1eec4302f0c51278c4dda44207e", "5", minter).WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	mock.ExpectCommit()
	mock.ExpectExec(`INSERT INTO nft_tokens\(collection, token_id, minter\)`).
		WithArgs("0x642eb8dca3e9e1eec4302f0c51278c4dda44207e", "5", minter).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	l := types.Log{Address: coll, Topics: []common.Hash{
		TopicTransfer721,
		common.Hash{}, // from = 0x0 → mint
		addrTopic(minter),
		common.BigToHash(big.NewInt(5)),
	}}
	if err := h.dispatch(context.Background(), l, 0); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRecordMintIgnoresNormalTransfers(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	h := &handlers{q: db.New(mock), bcast: sse.New()}
	// No expectations: a non-mint must issue no write at all.
	if err := h.recordMint(context.Background(), "0xa", "1", "0x00000000000000000000000000000000000000d2", "0x00000000000000000000000000000000000000c1"); err != nil {
		t.Fatal(err)
	}
	// Burns (to = 0x0) are not mints either.
	if err := h.recordMint(context.Background(), "0xa", "1", "0x0000000000000000000000000000000000000000", "0x0000000000000000000000000000000000000000"); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
