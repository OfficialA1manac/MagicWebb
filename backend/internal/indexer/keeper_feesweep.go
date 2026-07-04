package indexer

import (
	"context"
	cryptoecdsa "crypto/ecdsa"
	"math/big"
	"strings"
	"time"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
	"github.com/rs/zerolog/log"
)

// Zodiac Allowance Module v0.62.0 address. Canonical singleton address
// deployed at the same CREATE2 address on Flare (14), Songbird (19), and
// Coston2 (114). Verify bytecode at this address on your target chain before
// first use: https://github.com/gnosisguild/zodiac/blob/master/contracts/allowance/AllowanceModule.sol
const allowanceModuleAddr = "0xCFbFaC74C26F8647cBDb8c5caf80BB5b32E43134"

// executeAllowanceTransfer selector:
// keccak256("executeAllowanceTransfer(address,address,address,uint96,address,uint96,address,bytes)")
var executeAllowanceTransferSelector = crypto.Keccak256([]byte(
	"executeAllowanceTransfer(address,address,address,uint96,address,uint96,address,bytes)"))[:4]

// noncesSelector: keccak256("nonces(address,address)") — reads the Allowance
// Module's nonce mapping for (delegate, safe). Used to build the EIP-712
// typed data that the delegate signs.
var noncesSelector = crypto.Keccak256([]byte("nonces(address,address)"))[:4]

// encodeExecuteAllowanceTransfer ABI-encodes the executeAllowanceTransfer call
// with the given parameters. The _signature is a dynamic bytes tail — the head
// holds 7 static words + 1 offset word (total 8 head words = 256 bytes after
// the 4-byte selector).
func encodeExecuteAllowanceTransfer(
	safe, token, to common.Address,
	amount *big.Int,
	paymentToken common.Address,
	payment *big.Int,
	delegate common.Address,
	signature []byte,
) []byte {
	out := append([]byte(nil), executeAllowanceTransferSelector...)

	// Head: 7 static address/int96 words + 1 offset word for dynamic bytes.
	writeAddress := func(a common.Address) { out = append(out, common.LeftPadBytes(a.Bytes(), 32)...) }
	writeUint256 := func(n *big.Int) {
		b := make([]byte, 32)
		n.FillBytes(b)
		out = append(out, b...)
	}

	writeAddress(safe)
	writeAddress(token)
	writeAddress(to)
	writeUint256(amount)   // uint96 padded to 32
	writeAddress(paymentToken)
	writeUint256(payment) // uint96 padded to 32
	writeAddress(delegate)

	// Offset to signature dynamic bytes tail: 8 head words × 32 = 256.
	writeUint256(new(big.Int).SetUint64(256))

	// Tail: signature length + data (padded to 32 bytes).
	writeUint256(new(big.Int).SetUint64(uint64(len(signature))))
	out = append(out, signature...)
	if pad := len(signature) % 32; pad > 0 {
		out = append(out, make([]byte, 32-pad)...)
	}

	return out
}

// readAllowanceNonce fetches the current nonce from the Allowance Module for
// the (delegate, safe) pair. Uses eth_call against the module contract.
func (r *Runner) readAllowanceNonce(ctx context.Context, delegate, safe common.Address) (*big.Int, error) {
	data := append([]byte(nil), noncesSelector...)
	data = append(data, common.LeftPadBytes(delegate.Bytes(), 32)...)
	data = append(data, common.LeftPadBytes(safe.Bytes(), 32)...)

	modAddr := common.HexToAddress(allowanceModuleAddr)
	out, err := r.eth.CallContract(ctx, ethereum.CallMsg{To: &modAddr, Data: data}, nil)
	if err != nil {
		return nil, err
	}
	if len(out) < 32 {
		return big.NewInt(0), nil // non-existent → nonce = 0
	}
	return new(big.Int).SetBytes(out[:32]), nil
}

// buildAllowanceTransferTypedData builds the EIP-712 typed data the delegate
// must sign for the Allowance Module's executeAllowanceTransfer. The typed data
// follows the Zodiac Allowance Module's schema:
//
//	EIP712Domain(name:"AllowanceModule", version:"1.0.0", chainId, verifyingContract)
//	Transfer(safe, to, amount, token, paymentToken, payment, nonce)
//
// The delegate signs this with EIP-712, then the keeper broadcasts the signed
// transfer via executeAllowanceTransfer.
func (r *Runner) buildAllowanceTransferTypedData(
	safe, to, token, paymentToken common.Address,
	amount, payment, nonce *big.Int,
) apitypes.TypedData {
	return apitypes.TypedData{
		Types: apitypes.Types{
			"EIP712Domain": []apitypes.Type{
				{Name: "name", Type: "string"},
				{Name: "version", Type: "string"},
				{Name: "chainId", Type: "uint256"},
				{Name: "verifyingContract", Type: "address"},
			},
			"Transfer": []apitypes.Type{
				{Name: "safe", Type: "address"},
				{Name: "to", Type: "address"},
				{Name: "amount", Type: "uint96"},
				{Name: "token", Type: "address"},
				{Name: "paymentToken", Type: "address"},
				{Name: "payment", Type: "uint96"},
				{Name: "nonce", Type: "uint256"},
			},
		},
		PrimaryType: "Transfer",
		Domain: apitypes.TypedDataDomain{
			Name:              "AllowanceModule",
			Version:           "1.0.0",
			ChainId:           math.NewHexOrDecimal256(int64(r.cfg.ChainID)),
			VerifyingContract: allowanceModuleAddr,
		},
		Message: apitypes.TypedDataMessage{
			"safe":         safe.Hex(),
			"to":           to.Hex(),
			"amount":       amount.String(),
			"token":        token.Hex(),
			"paymentToken": paymentToken.Hex(),
			"payment":      payment.String(),
			"nonce":        nonce.String(),
		},
	}
}

// signTypedData signs EIP-712 typed data with the given private key.
// Returns the 65-byte [R || S || V] signature with v in [27, 28].
func signTypedData(typedData apitypes.TypedData, key *cryptoecdsa.PrivateKey) ([]byte, error) {
	domainSeparator, err := typedData.HashStruct("EIP712Domain", typedData.Domain.Map())
	if err != nil {
		return nil, err
	}
	typedHash, err := typedData.HashStruct(typedData.PrimaryType, typedData.Message)
	if err != nil {
		return nil, err
	}
	rawData := []byte{0x19, 0x01}
	rawData = append(rawData, domainSeparator...)
	rawData = append(rawData, typedHash...)
	hash := crypto.Keccak256(rawData)

	sig, err := crypto.Sign(hash, key)
	if err != nil {
		return nil, err
	}
	// crypto.Sign returns v in {0, 1}. EIP-712 (and Ethereum tx signing)
	// expects v in {27, 28}. Adjust: 0→27, 1→28.
	if sig[64] < 27 {
		sig[64] += 27
	}
	return sig, nil
}

// ── Fee Sweeper ───────────────────────────────────────────────────────────
//
// The Gnosis Safe accumulates marketplace fees (feeRecipient = Safe address).
// The Zodiac Allowance Module (canonical, audited Safe ecosystem module)
// authorizes a delegate (the keeper) to pull funds from the Safe up to a
// pre-set allowance. The keeper signs an EIP-712 typed transfer and broadcasts
// executeAllowanceTransfer on the Allowance Module contract.
//
// One-time on-chain setup (after DeploySafe.s.sol runs):
//
//	1. enableModule(allowanceModuleAddr) on the Safe
//	2. addDelegate(KEEPER_ADDR) on the Allowance Module
//	3. setAllowance(KEEPER_ADDR, address(0), <periodAmount>, <periodInSeconds>, 0)
//
// Steps 1-3 require Safe-threshold signatures once. After that, the keeper
// can pull within the allowance without further signatures.

const feeSweepGas = 80_000

// runFeeSweeper periodically checks the Gnosis Safe's native balance. When the
// balance exceeds FeeSweepMinWei, it signs and broadcasts a transfer to the
// personal wallet via the Zodiac Allowance Module. Runs on the same keeper
// lifecycle (single-flight gate) as runAuctionKeeper and runLoserRefundSweeper.
func (r *Runner) runFeeSweeper(ctx context.Context) {
	keeperKeyHex := strings.TrimPrefix(r.cfg.KeeperKey, "0x")
	key, err := crypto.HexToECDSA(keeperKeyHex)
	if err != nil {
		log.Error().Err(err).Msg("fee sweeper: invalid KEEPER_KEY, disabled")
		return
	}
	keeperAddr := crypto.PubkeyToAddress(key.PublicKey)
	safeAddr := common.HexToAddress(r.cfg.SafeAddr)
	walletAddr := common.HexToAddress(r.cfg.PersonalWalletAddr)
	modAddr := common.HexToAddress(allowanceModuleAddr)
	chainIDBig := big.NewInt(int64(r.cfg.ChainID))
	signer := types.NewLondonSigner(chainIDBig)

	// Parse dust threshold.
	dustStr := r.cfg.FeeSweepMinWei
	dustThreshold := new(big.Int)
	if dustStr != "" {
		dustThreshold, _ = new(big.Int).SetString(dustStr, 10)
	}
	if dustThreshold == nil || dustThreshold.Sign() <= 0 {
		dustThreshold = new(big.Int).SetUint64(100_000_000_000_000_000) // 0.1 native token
	}

	log.Info().
		Str("keeper", keeperAddr.Hex()).
		Str("safe", safeAddr.Hex()).
		Str("wallet", walletAddr.Hex()).
		Str("dust_wei", dustThreshold.String()).
		Msg("fee sweeper: started — requires on-chain Allowance Module setup on Safe")

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.sweepFee(ctx, key, keeperAddr, safeAddr, walletAddr, modAddr, signer, chainIDBig, dustThreshold)
		}
	}
}

func (r *Runner) sweepFee(
	ctx context.Context,
	key *cryptoecdsa.PrivateKey,
	keeperAddr, safeAddr, walletAddr, modAddr common.Address,
	signer types.Signer,
	chainID *big.Int,
	dustThreshold *big.Int,
) {
	// 1. Check Safe native balance.
	balCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	balance, err := r.eth.BalanceAt(balCtx, safeAddr, nil)
	if err != nil {
		log.Warn().Err(err).Str("safe", safeAddr.Hex()).Msg("fee sweeper: BalanceAt failed")
		return
	}
	if balance.Cmp(dustThreshold) <= 0 {
		return // nothing to sweep
	}

	// 2. Read the current nonce for (keeper, safe) from the Allowance Module.
	nonce, err := r.readAllowanceNonce(ctx, keeperAddr, safeAddr)
	if err != nil {
		log.Warn().Err(err).Str("safe", safeAddr.Hex()).Msg("fee sweeper: read nonce failed")
		return
	}

	// 3. Build EIP-712 typed data and sign with keeper key.
	zeroAddr := common.Address{}
	token := zeroAddr // address(0) = native token
	paymentToken := zeroAddr
	payment := new(big.Int)

	typedData := r.buildAllowanceTransferTypedData(safeAddr, walletAddr, token, paymentToken, balance, payment, nonce)
	sig, err := signTypedData(typedData, key)
	if err != nil {
		log.Error().Err(err).Str("safe", safeAddr.Hex()).Msg("fee sweeper: EIP-712 signing failed")
		return
	}

	// 4. Build executeAllowanceTransfer calldata and broadcast.
	data := encodeExecuteAllowanceTransfer(safeAddr, token, walletAddr, balance, paymentToken, payment, keeperAddr, sig)
	txHash, err := r.sendRaw(ctx, key, keeperAddr, modAddr, signer, chainID, data, feeSweepGas)
	if err != nil {
		log.Error().Err(err).Str("safe", safeAddr.Hex()).Msg("fee sweeper: executeAllowanceTransfer tx failed")
		return
	}
	if err := r.waitMined(ctx, txHash, 2*time.Minute); err != nil {
		log.Error().Err(err).Str("safe", safeAddr.Hex()).Str("tx", txHash.Hex()).
			Msg("fee sweeper: tx not confirmed; will retry on next tick")
		return
	}

	balFLR, _ := new(big.Float).Quo(new(big.Float).SetInt(balance), new(big.Float).SetFloat64(1e18)).Float64()
	log.Info().
		Str("safe", safeAddr.Hex()).
		Str("wallet", walletAddr.Hex()).
		Float64("amount", balFLR).
		Str("tx", txHash.Hex()).
		Msg("fee sweeper: fees swept from Safe to personal wallet")
}
