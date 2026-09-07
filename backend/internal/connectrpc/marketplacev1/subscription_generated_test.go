package marketplacev1_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"

	marketplacev1 "github.com/OfficialA1manac/MagicWebb/backend/internal/connectrpc/marketplacev1"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/connectrpc/marketplacev1/marketplacev1connect"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/db"
	"github.com/OfficialA1manac/MagicWebb/backend/internal/sse"
)

// v3.6 wave 6: the subscription RPCs are declared in marketplace.proto and
// served by the GENERATED handler, so a generated client can open the stream
// and receive a published event end-to-end over HTTP.
func TestGeneratedSubscribeListingsStream(t *testing.T) {
	bcast := sse.New()
	srv := marketplacev1.NewServer(nil, bcast)
	mux := http.NewServeMux()
	mux.Handle(marketplacev1connect.NewMarketplaceServiceHandler(srv))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	client := marketplacev1connect.NewMarketplaceServiceClient(ts.Client(), ts.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// connect-go flushes a server stream's headers on the first Send, so the
	// open call below only returns once the handler has an event to deliver.
	// Publish continuously until the stream is consumed: a sibling
	// collection's event (must be filtered out) followed by the wanted one.
	done := make(chan struct{})
	go func() {
		tk := time.NewTicker(50 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-done:
				return
			case <-tk.C:
				bcast.Publish(sse.Event{Type: "listing-updated", Data: db.ListingRow{Collection: "0xabc0000000000000000000000000000000000002", TokenID: "9", Seller: "0xs", PriceWei: "1"}})
				bcast.Publish(sse.Event{Type: "listing-updated", Data: db.ListingRow{Collection: "0xabc0000000000000000000000000000000000001", TokenID: "7", Seller: "0xseller", PriceWei: "5000000000000000000", Name: "Meadow #7"}})
			}
		}
	}()
	defer close(done)

	stream, err := client.SubscribeListings(ctx, connect.NewRequest(&marketplacev1.SubscribeListingsRequest{
		Collection: "0xAbC0000000000000000000000000000000000001",
	}))
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer stream.Close()

	if !stream.Receive() {
		t.Fatalf("no message received: %v", stream.Err())
	}
	got := stream.Msg().GetListing()
	if got == nil || got.GetTokenId() != "7" || got.GetSeller() != "0xseller" || got.GetName() != "Meadow #7" {
		t.Fatalf("unexpected listing: %+v", got)
	}
	cancel()
}

// SubscribeNotifications refuses anonymous callers at the generated boundary.
func TestGeneratedSubscribeNotificationsRequiresAuth(t *testing.T) {
	bcast := sse.New()
	srv := marketplacev1.NewServerWithAuth(nil, bcast, "0123456789abcdef0123456789abcdef")
	mux := http.NewServeMux()
	mux.Handle(marketplacev1connect.NewMarketplaceServiceHandler(srv))
	ts := httptest.NewServer(mux)
	defer ts.Close()

	client := marketplacev1connect.NewMarketplaceServiceClient(ts.Client(), ts.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.SubscribeNotifications(ctx, connect.NewRequest(&marketplacev1.SubscribeNotificationsRequest{}))
	if err != nil {
		if connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("want Unauthenticated, got %v", err)
		}
		return
	}
	defer stream.Close()
	if stream.Receive() {
		t.Fatal("anonymous caller must not receive notifications")
	}
	if connect.CodeOf(stream.Err()) != connect.CodeUnauthenticated {
		t.Fatalf("want Unauthenticated, got %v", stream.Err())
	}
}
