package domain

import "time"

type User struct {
	Address   string
	Username  *string
	Bio       *string
	AvatarURL *string
	CreatedAt time.Time
}

type Collection struct {
	Address  string
	Name     string
	Symbol   *string
	Verified bool
}

type Token struct {
	Collection  string
	TokenID     string
	Owner       string
	MetadataURI *string
	ImageURL    *string
	Name        *string
}

type Listing struct {
	ID        int64
	Coll      string
	TokenID   string
	Seller    string
	PriceWei  string
	ExpiresAt time.Time
	Status    string
}

type Offer struct {
	ID        int64
	Coll      string
	TokenID   *string
	Bidder    string
	AmountWei string
	ExpiresAt time.Time
	Signature []byte
	Nonce     string
	Status    string
}
