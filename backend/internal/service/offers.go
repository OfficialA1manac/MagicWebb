package service

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/OfficialA1manac/MagicWebb/backend/gen/common/v1"
	offersv1 "github.com/OfficialA1manac/MagicWebb/backend/gen/offers/v1"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/auth"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/cache"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/config"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
)

// OffersService implements offersv1.OffersServiceServer.
type OffersService struct {
	offersv1.UnimplementedOffersServiceServer
	q   *db.Q
	rdb *cache.Client
	cfg *config.Config
}

// NewOffersService constructs the offers gRPC service.
func NewOffersService(q *db.Q, rdb *cache.Client, cfg *config.Config) *OffersService {
	return &OffersService{q: q, rdb: rdb, cfg: cfg}
}

func (s *OffersService) GetOffer(ctx context.Context, req *offersv1.GetOfferRequest) (*offersv1.Offer, error) {
	r, err := s.q.GetOffer(ctx, req.GetOfferId())
	if err != nil {
		return nil, status.Error(codes.NotFound, "offer not found")
	}
	return offerRowToProto(r), nil
}

func (s *OffersService) ListOffers(ctx context.Context, req *offersv1.ListOffersRequest) (*offersv1.ListOffersResponse, error) {
	f := db.OffersFilter{Limit: int(req.GetPagination().GetLimit())}
	if req.GetCollection() != nil {
		f.Collection = req.GetCollection().GetValue()
	}
	if req.GetTokenId() != nil {
		f.TokenID = req.GetTokenId().GetValue()
	}
	if req.GetBidder() != nil {
		f.Bidder = req.GetBidder().GetValue()
	}
	if req.GetOwner() != nil {
		f.Owner = req.GetOwner().GetValue()
	}
	switch req.GetStatus() {
	case offersv1.OfferStatus_PENDING:
		f.Status = "pending"
	case offersv1.OfferStatus_ACCEPTED:
		f.Status = "accepted"
	case offersv1.OfferStatus_CANCELLED:
		f.Status = "cancelled"
	case offersv1.OfferStatus_EXPIRED:
		f.Status = "expired"
	}
	rows, err := s.q.ListOffers(ctx, f)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list offers: %v", err)
	}
	resp := &offersv1.ListOffersResponse{}
	for _, r := range rows {
		resp.Offers = append(resp.Offers, offerRowToProto(&r))
	}
	return resp, nil
}

func (s *OffersService) NotifyOffer(ctx context.Context, req *offersv1.NotifyOfferRequest) (*offersv1.NotifyOfferResponse, error) {
	offer := req.GetOffer()
	if offer == nil {
		return nil, status.Error(codes.InvalidArgument, "offer required")
	}

	// Caller must be authenticated (JWT injected by auth interceptor).
	caller, ok := auth.CallerFromCtx(ctx)
	if !ok {
		// Also accept from gRPC metadata directly if caller not in context.
		md, _ := metadata.FromIncomingContext(ctx)
		vals := md.Get("x-caller")
		if len(vals) > 0 {
			caller = vals[0]
		}
	}
	if caller == "" {
		return nil, status.Error(codes.Unauthenticated, "authentication required")
	}

	if offer.GetBidder() == nil || offer.GetCollection() == nil {
		return nil, status.Error(codes.InvalidArgument, "bidder and collection required")
	}

	bidder := offer.GetBidder().GetValue()
	collection := offer.GetCollection().GetValue()

	// Verify caller is the offer's bidder.
	if !strings.EqualFold(caller, bidder) {
		return nil, status.Error(codes.PermissionDenied, "caller must be the bidder")
	}

	// Validate EIP-712 signature.
	amtWei := new(big.Int).SetBytes(offer.GetAmount().GetValue())
	tokenIDStr := ""
	if offer.GetTokenId() != nil {
		tokenIDStr = offer.GetTokenId().GetValue()
	}
	tokenID, _ := new(big.Int).SetString(tokenIDStr, 10)
	nonce := new(big.Int).SetUint64(offer.GetNonce())
	var expiresAt uint64
	if offer.GetExpiresAt() != nil {
		expiresAt = uint64(offer.GetExpiresAt().GetSeconds())
	}

	sig := offer.GetSignature()
	if err := verifyOfferSig(s.cfg, collection, tokenID, bidder, amtWei, nonce, expiresAt, sig); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid signature: %v", err)
	}

	// Store offer.
	expiresTime := time.Unix(int64(expiresAt), 0)
	r := db.OfferRow{
		Bidder:     bidder,
		Collection: collection,
		TokenID:    tokenIDStr,
		AmountWei:  amtWei.String(),
		Nonce:      nonce.String(),
		ExpiresAt:  expiresTime,
		Signature:  sig,
		Status:     "pending",
	}
	offerID, err := s.q.InsertOffer(ctx, r)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "store offer: %v", err)
	}

	// Notify via Redis for real-time subscription.
	payload, _ := json.Marshal(map[string]any{
		"event":      "OfferNotified",
		"offerId":    offerID,
		"collection": collection,
		"tokenId":    tokenIDStr,
		"bidder":     bidder,
		"amtWei":     amtWei.String(),
	})
	if err := s.rdb.Publish(ctx, "offers:events", string(payload)); err != nil {
		log.Warn().Err(err).Msg("notify offer: redis publish failed")
	}

	return &offersv1.NotifyOfferResponse{OfferId: offerID}, nil
}

func (s *OffersService) StreamOfferEvents(_ *emptypb.Empty, stream grpc.ServerStreamingServer[offersv1.OfferEvent]) error {
	ctx := stream.Context()
	sub := s.rdb.Subscribe(ctx, "offers:events")
	defer sub.Close()
	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-ch:
			if !ok {
				return status.Error(codes.Unavailable, "event stream closed")
			}
			var raw map[string]any
			if err := json.Unmarshal([]byte(msg.Payload), &raw); err != nil {
				log.Warn().Err(err).Msg("offers stream: bad payload")
				continue
			}
			evt := &offersv1.OfferEvent{
				Type:       eventTypeFromString(raw["event"]),
				OccurredAt: timestamppb.New(time.Now()),
			}
			if err := stream.Send(evt); err != nil {
				return err
			}
		}
	}
}

// ── EIP-712 verification ──────────────────────────────────────────────────────

var offerTypeHash = crypto.Keccak256Hash([]byte(
	"Offer(address collection,uint256 tokenId,address bidder,uint128 amountWei,uint256 nonce,uint64 expiresAt)",
))

var domainTypeHash = crypto.Keccak256Hash([]byte(
	"EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)",
))

var nameHash = crypto.Keccak256Hash([]byte("WebbPlace"))
var versionHash = crypto.Keccak256Hash([]byte("1"))

func domainSeparator(chainID uint64, offerBookAddr common.Address) common.Hash {
	chainIDBig := new(big.Int).SetUint64(chainID)
	encoded := make([]byte, 32*5)
	copy(encoded[0:32], domainTypeHash.Bytes())
	copy(encoded[32:64], nameHash.Bytes())
	copy(encoded[64:96], versionHash.Bytes())
	chainIDBig.FillBytes(encoded[96:128])
	copy(encoded[128:160], common.LeftPadBytes(offerBookAddr.Bytes(), 32))
	return crypto.Keccak256Hash(encoded)
}

func offerStructHash(collection common.Address, tokenID *big.Int, bidder common.Address, amountWei, nonce *big.Int, expiresAt uint64) common.Hash {
	encoded := make([]byte, 32*7)
	copy(encoded[0:32], offerTypeHash.Bytes())
	copy(encoded[32:64], common.LeftPadBytes(collection.Bytes(), 32))
	if tokenID != nil {
		tokenID.FillBytes(encoded[64:96])
	}
	copy(encoded[96:128], common.LeftPadBytes(bidder.Bytes(), 32))
	if amountWei != nil {
		amountWei.FillBytes(encoded[128:160])
	}
	if nonce != nil {
		nonce.FillBytes(encoded[160:192])
	}
	new(big.Int).SetUint64(expiresAt).FillBytes(encoded[192:224])
	return crypto.Keccak256Hash(encoded)
}

func verifyOfferSig(cfg *config.Config, collection string, tokenID *big.Int, bidder string, amtWei, nonce *big.Int, expiresAt uint64, sigHex string) error {
	if tokenID == nil {
		tokenID = new(big.Int)
	}
	ds := domainSeparator(cfg.ChainID, common.HexToAddress(cfg.OfferBookAddr))
	sh := offerStructHash(
		common.HexToAddress(collection),
		tokenID,
		common.HexToAddress(bidder),
		amtWei,
		nonce,
		expiresAt,
	)
	// EIP-712 final hash
	final := make([]byte, 2+32+32)
	final[0] = 0x19
	final[1] = 0x01
	copy(final[2:34], ds.Bytes())
	copy(final[34:66], sh.Bytes())
	hash := crypto.Keccak256Hash(final)

	// Decode and normalize signature
	sigBytes, err := hexDecode(sigHex)
	if err != nil || len(sigBytes) != 65 {
		return fmt.Errorf("invalid signature encoding")
	}
	sig := make([]byte, 65)
	copy(sig, sigBytes)
	if sig[64] >= 27 {
		sig[64] -= 27
	}

	pubKey, err := crypto.SigToPub(hash.Bytes(), sig)
	if err != nil {
		return fmt.Errorf("sig recovery failed: %w", err)
	}
	recovered := crypto.PubkeyToAddress(*pubKey)
	if !strings.EqualFold(recovered.Hex(), bidder) {
		return fmt.Errorf("signer mismatch: got %s, want %s", recovered.Hex(), bidder)
	}
	return nil
}

func hexDecode(s string) ([]byte, error) {
	s = strings.TrimPrefix(s, "0x")
	return hex.DecodeString(s)
}

// ── proto conversion ──────────────────────────────────────────────────────────

func offerRowToProto(r *db.OfferRow) *offersv1.Offer {
	amtWei, _ := new(big.Int).SetString(r.AmountWei, 10)
	nonce, _ := new(big.Int).SetString(r.Nonce, 10)
	var amtBytes []byte
	if amtWei != nil {
		amtBytes = amtWei.Bytes()
	}
	var offerStatus offersv1.OfferStatus
	switch r.Status {
	case "pending":
		offerStatus = offersv1.OfferStatus_PENDING
	case "accepted":
		offerStatus = offersv1.OfferStatus_ACCEPTED
	case "cancelled":
		offerStatus = offersv1.OfferStatus_CANCELLED
	case "expired":
		offerStatus = offersv1.OfferStatus_EXPIRED
	}
	o := &offersv1.Offer{
		OfferId:    r.OfferID,
		Bidder:     &commonv1.Address{Value: r.Bidder},
		Collection: &commonv1.Address{Value: r.Collection},
		Amount:     &commonv1.Wei{Value: amtBytes},
		Signature:  r.Signature,
		Status:     offerStatus,
		ExpiresAt:  timestamppb.New(r.ExpiresAt),
		CreatedAt:  timestamppb.New(r.CreatedAt),
	}
	if r.TokenID != "" {
		o.TokenId = &commonv1.TokenId{Value: r.TokenID}
	}
	if nonce != nil {
		o.Nonce = nonce.Uint64()
	}
	return o
}
