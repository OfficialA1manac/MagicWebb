// GQL-4: GraphQL subscription execution dispatch.
//
// This file provides the runtime bridge between gqlgen's transport.Websocket
// (which drives the graphql-transport-ws protocol) and the subscription
// resolvers in resolver.go. The resolvers subscribe to the SSE Broadcaster
// and return <-chan *Type; the methods below manage the channe/fan-out and
// JSON marshalling so gqlgen's transport layer can stream results to clients
// without polling.
//
// When gqlgen is re-run with a schema that includes subscription fields, the
// generated.go will contain equivalent auto-generated code. At that point this
// file can be removed.

package graphql

import (
	"context"
	"fmt"
	"io"
	"strconv"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
)

// _Subscription dispatches a GraphQL subscription operation to the correct
// field resolver. GraphQL mandates exactly one root field in a subscription,
// so this validates that constraint and routes to the corresponding
// _Subscription_<field> method.
//
// Returns a function that produces graphql.Marshaler values. Each call to the
// returned function blocks on the next event from the underlying channel.
// When the channel closes (SSE broadcaster cancelled) or the context is
// cancelled (client disconnects), the function returns nil, signalling
// gqlgen's transport to send a "complete" message.
func (ec *executionContext) _Subscription(ctx context.Context, sel ast.SelectionSet) func(context.Context) graphql.Marshaler {
	fields := graphql.CollectFields(ec.OperationContext, sel, subscriptionImplementors)
	ctx = graphql.WithFieldContext(ctx, &graphql.FieldContext{
		Object: "Subscription",
	})
	if len(fields) != 1 {
		ec.Error(ctx, fmt.Errorf("subscriptions must have exactly one root field"))
		return nil
	}
	switch fields[0].Name {
	case "listingUpdated":
		return ec._Subscription_listingUpdated(ctx, fields[0])
	case "auctionUpdated":
		return ec._Subscription_auctionUpdated(ctx, fields[0])
	case "activityUpdated":
		return ec._Subscription_activityUpdated(ctx, fields[0])
	case "notificationUpdated":
		return ec._Subscription_notificationUpdated(ctx, fields[0])
	default:
		panic("unknown subscription field " + strconv.Quote(fields[0].Name))
	}
}

// ── listingUpdated ──────────────────────────────────────────────────────────

// _Subscription_listingUpdated calls the resolver's ListingUpdated method,
// which returns a <-chan *Listing backed by the SSE Broadcaster. The returned
// closure reads from the channel and produces a graphql.Marshaler for each
// event. When the channel closes or ctx is cancelled, it returns nil.
func (ec *executionContext) _Subscription_listingUpdated(ctx context.Context, field graphql.CollectedField) func(context.Context) graphql.Marshaler {
	rawArgs := field.ArgumentMap(ec.Variables)
	args, err := ec.field_Subscription_listingUpdated_args(ctx, rawArgs)
	if err != nil {
		ec.Error(ctx, err)
		return nil
	}
	ctx = graphql.WithFieldContext(ctx, &graphql.FieldContext{
		Object: "Subscription",
		Field:  field,
		Args:   args,
	})

	results, err := ec.resolvers.Subscription().ListingUpdated(ctx, args["collection"].(*string), args["tokenID"].(*string))
	if err != nil || results == nil {
		if err != nil {
			ec.Error(ctx, err)
		}
		return nil
	}

	return func(ctx context.Context) graphql.Marshaler {
		select {
		case <-ctx.Done():
			return nil
		case data, ok := <-results:
			if !ok {
				return nil
			}
			return graphql.WriterFunc(func(w io.Writer) {
				w.Write([]byte(`{"listingUpdated":`))
				ec._Listing(ctx, field.Selections, data).MarshalGQL(w)
				w.Write([]byte(`}`))
			})
		}
	}
}

func (ec *executionContext) fieldContext_Subscription_listingUpdated(ctx context.Context, field graphql.CollectedField) (fc *graphql.FieldContext, err error) {
	fc = &graphql.FieldContext{
		Object:     "Subscription",
		Field:      field,
		IsMethod:   true,
		IsResolver: true,
		Child: func(ctx context.Context, field graphql.CollectedField) (*graphql.FieldContext, error) {
			switch field.Name {
			case "collection":
				return ec.fieldContext_Listing_collection(ctx, field)
			case "tokenID":
				return ec.fieldContext_Listing_tokenID(ctx, field)
			case "seller":
				return ec.fieldContext_Listing_seller(ctx, field)
			case "priceWei":
				return ec.fieldContext_Listing_priceWei(ctx, field)
			case "amount":
				return ec.fieldContext_Listing_amount(ctx, field)
			case "standard":
				return ec.fieldContext_Listing_standard(ctx, field)
			case "expiresAt":
				return ec.fieldContext_Listing_expiresAt(ctx, field)
			case "listedAt":
				return ec.fieldContext_Listing_listedAt(ctx, field)
			case "txHash":
				return ec.fieldContext_Listing_txHash(ctx, field)
			case "name":
				return ec.fieldContext_Listing_name(ctx, field)
			case "imageURI":
				return ec.fieldContext_Listing_imageURI(ctx, field)
			case "collectionVerified":
				return ec.fieldContext_Listing_collectionVerified(ctx, field)
			}
			return nil, fmt.Errorf("no field named %q was found under type Listing", field.Name)
		},
	}
	defer func() {
		if r := recover(); r != nil {
			err = ec.Recover(ctx, r)
			ec.Error(ctx, err)
		}
	}()
	ctx = graphql.WithFieldContext(ctx, fc)
	if fc.Args, err = ec.field_Subscription_listingUpdated_args(ctx, field.ArgumentMap(ec.Variables)); err != nil {
		ec.Error(ctx, err)
		return fc, err
	}
	return fc, nil
}

func (ec *executionContext) field_Subscription_listingUpdated_args(ctx context.Context, rawArgs map[string]any) (map[string]any, error) {
	var err error
	args := map[string]any{}
	arg0, err := ec.field_Subscription_listingUpdated_argsCollection(ctx, rawArgs)
	if err != nil {
		return nil, err
	}
	args["collection"] = arg0
	arg1, err := ec.field_Subscription_listingUpdated_argsTokenID(ctx, rawArgs)
	if err != nil {
		return nil, err
	}
	args["tokenID"] = arg1
	return args, nil
}

func (ec *executionContext) field_Subscription_listingUpdated_argsCollection(
	ctx context.Context,
	rawArgs map[string]any,
) (*string, error) {
	if _, ok := rawArgs["collection"]; !ok {
		var zeroVal *string
		return zeroVal, nil
	}
	ctx = graphql.WithPathContext(ctx, graphql.NewPathWithField("collection"))
	if tmp, ok := rawArgs["collection"]; ok {
		return ec.unmarshalOString2ᚖstring(ctx, tmp)
	}
	var zeroVal *string
	return zeroVal, nil
}

func (ec *executionContext) field_Subscription_listingUpdated_argsTokenID(
	ctx context.Context,
	rawArgs map[string]any,
) (*string, error) {
	if _, ok := rawArgs["tokenID"]; !ok {
		var zeroVal *string
		return zeroVal, nil
	}
	ctx = graphql.WithPathContext(ctx, graphql.NewPathWithField("tokenID"))
	if tmp, ok := rawArgs["tokenID"]; ok {
		return ec.unmarshalOString2ᚖstring(ctx, tmp)
	}
	var zeroVal *string
	return zeroVal, nil
}

// ── auctionUpdated ──────────────────────────────────────────────────────────

// _Subscription_auctionUpdated calls the resolver's AuctionUpdated method,
// which returns a <-chan *Auction backed by the SSE Broadcaster.
func (ec *executionContext) _Subscription_auctionUpdated(ctx context.Context, field graphql.CollectedField) func(context.Context) graphql.Marshaler {
	rawArgs := field.ArgumentMap(ec.Variables)
	args, err := ec.field_Subscription_auctionUpdated_args(ctx, rawArgs)
	if err != nil {
		ec.Error(ctx, err)
		return nil
	}
	ctx = graphql.WithFieldContext(ctx, &graphql.FieldContext{
		Object: "Subscription",
		Field:  field,
		Args:   args,
	})

	results, err := ec.resolvers.Subscription().AuctionUpdated(ctx, args["auctionId"].(*int))
	if err != nil || results == nil {
		if err != nil {
			ec.Error(ctx, err)
		}
		return nil
	}

	return func(ctx context.Context) graphql.Marshaler {
		select {
		case <-ctx.Done():
			return nil
		case data, ok := <-results:
			if !ok {
				return nil
			}
			return graphql.WriterFunc(func(w io.Writer) {
				w.Write([]byte(`{"auctionUpdated":`))
				ec._Auction(ctx, field.Selections, data).MarshalGQL(w)
				w.Write([]byte(`}`))
			})
		}
	}
}

func (ec *executionContext) fieldContext_Subscription_auctionUpdated(ctx context.Context, field graphql.CollectedField) (fc *graphql.FieldContext, err error) {
	fc = &graphql.FieldContext{
		Object:     "Subscription",
		Field:      field,
		IsMethod:   true,
		IsResolver: true,
		Child: func(ctx context.Context, field graphql.CollectedField) (*graphql.FieldContext, error) {
			switch field.Name {
			case "auctionID":
				return ec.fieldContext_Auction_auctionID(ctx, field)
			case "collection":
				return ec.fieldContext_Auction_collection(ctx, field)
			case "tokenID":
				return ec.fieldContext_Auction_tokenID(ctx, field)
			case "seller":
				return ec.fieldContext_Auction_seller(ctx, field)
			case "standard":
				return ec.fieldContext_Auction_standard(ctx, field)
			case "reservePriceWei":
				return ec.fieldContext_Auction_reservePriceWei(ctx, field)
			case "highestBidWei":
				return ec.fieldContext_Auction_highestBidWei(ctx, field)
			case "highestBidder":
				return ec.fieldContext_Auction_highestBidder(ctx, field)
			case "minIncrementBps":
				return ec.fieldContext_Auction_minIncrementBps(ctx, field)
			case "startsAt":
				return ec.fieldContext_Auction_startsAt(ctx, field)
			case "endsAt":
				return ec.fieldContext_Auction_endsAt(ctx, field)
			case "status":
				return ec.fieldContext_Auction_status(ctx, field)
			case "createTx":
				return ec.fieldContext_Auction_createTx(ctx, field)
			case "name":
				return ec.fieldContext_Auction_name(ctx, field)
			case "imageURI":
				return ec.fieldContext_Auction_imageURI(ctx, field)
			case "bids":
				return ec.fieldContext_Auction_bids(ctx, field)
			case "effectiveBids":
				return ec.fieldContext_Auction_effectiveBids(ctx, field)
			}
			return nil, fmt.Errorf("no field named %q was found under type Auction", field.Name)
		},
	}
	defer func() {
		if r := recover(); r != nil {
			err = ec.Recover(ctx, r)
			ec.Error(ctx, err)
		}
	}()
	ctx = graphql.WithFieldContext(ctx, fc)
	if fc.Args, err = ec.field_Subscription_auctionUpdated_args(ctx, field.ArgumentMap(ec.Variables)); err != nil {
		ec.Error(ctx, err)
		return fc, err
	}
	return fc, nil
}

func (ec *executionContext) field_Subscription_auctionUpdated_args(ctx context.Context, rawArgs map[string]any) (map[string]any, error) {
	var err error
	args := map[string]any{}
	arg0, err := ec.field_Subscription_auctionUpdated_argsAuctionID(ctx, rawArgs)
	if err != nil {
		return nil, err
	}
	args["auctionId"] = arg0
	return args, nil
}

func (ec *executionContext) field_Subscription_auctionUpdated_argsAuctionID(
	ctx context.Context,
	rawArgs map[string]any,
) (*int, error) {
	if _, ok := rawArgs["auctionId"]; !ok {
		var zeroVal *int
		return zeroVal, nil
	}
	ctx = graphql.WithPathContext(ctx, graphql.NewPathWithField("auctionId"))
	if tmp, ok := rawArgs["auctionId"]; ok {
		return ec.unmarshalOInt2ᚖint(ctx, tmp)
	}
	var zeroVal *int
	return zeroVal, nil
}

// ── activityUpdated ─────────────────────────────────────────────────────────

// _Subscription_activityUpdated calls the resolver's ActivityUpdated method,
// which returns a <-chan *Activity backed by the SSE Broadcaster.
func (ec *executionContext) _Subscription_activityUpdated(ctx context.Context, field graphql.CollectedField) func(context.Context) graphql.Marshaler {
	ctx = graphql.WithFieldContext(ctx, &graphql.FieldContext{
		Object: "Subscription",
		Field:  field,
	})

	results, err := ec.resolvers.Subscription().ActivityUpdated(ctx)
	if err != nil || results == nil {
		if err != nil {
			ec.Error(ctx, err)
		}
		return nil
	}

	return func(ctx context.Context) graphql.Marshaler {
		select {
		case <-ctx.Done():
			return nil
		case data, ok := <-results:
			if !ok {
				return nil
			}
			return graphql.WriterFunc(func(w io.Writer) {
				w.Write([]byte(`{"activityUpdated":`))
				ec._Activity(ctx, field.Selections, data).MarshalGQL(w)
				w.Write([]byte(`}`))
			})
		}
	}
}

func (ec *executionContext) fieldContext_Subscription_activityUpdated(ctx context.Context, field graphql.CollectedField) (fc *graphql.FieldContext, err error) {
	fc = &graphql.FieldContext{
		Object:     "Subscription",
		Field:      field,
		IsMethod:   true,
		IsResolver: true,
		Child: func(ctx context.Context, field graphql.CollectedField) (*graphql.FieldContext, error) {
			switch field.Name {
			case "type":
				return ec.fieldContext_Activity_type(ctx, field)
			case "collection":
				return ec.fieldContext_Activity_collection(ctx, field)
			case "tokenID":
				return ec.fieldContext_Activity_tokenID(ctx, field)
			case "amountWei":
				return ec.fieldContext_Activity_amountWei(ctx, field)
			case "timestamp":
				return ec.fieldContext_Activity_timestamp(ctx, field)
			case "txHash":
				return ec.fieldContext_Activity_txHash(ctx, field)
			}
			return nil, fmt.Errorf("no field named %q was found under type Activity", field.Name)
		},
	}
	return fc, nil
}

// ── notificationUpdated ─────────────────────────────────────────────────────

// _Subscription_notificationUpdated calls the resolver's NotificationUpdated
// method, which returns a <-chan *Notification backed by the SSE Broadcaster.
// Notifications are gated by Phase 3 RBAC: the resolver extracts the
// authenticated wallet from ctx (set by HandleWS via AuthCtxKey) and only
// forwards notifications belonging to that user.
func (ec *executionContext) _Subscription_notificationUpdated(ctx context.Context, field graphql.CollectedField) func(context.Context) graphql.Marshaler {
	ctx = graphql.WithFieldContext(ctx, &graphql.FieldContext{
		Object: "Subscription",
		Field:  field,
	})

	results, err := ec.resolvers.Subscription().NotificationUpdated(ctx)
	if err != nil || results == nil {
		if err != nil {
			ec.Error(ctx, err)
		}
		return nil
	}

	return func(ctx context.Context) graphql.Marshaler {
		select {
		case <-ctx.Done():
			return nil
		case data, ok := <-results:
			if !ok {
				return nil
			}
			return graphql.WriterFunc(func(w io.Writer) {
				w.Write([]byte(`{"notificationUpdated":`))
				ec._Notification(ctx, field.Selections, data).MarshalGQL(w)
				w.Write([]byte(`}`))
			})
		}
	}
}

func (ec *executionContext) fieldContext_Subscription_notificationUpdated(ctx context.Context, field graphql.CollectedField) (fc *graphql.FieldContext, err error) {
	fc = &graphql.FieldContext{
		Object:     "Subscription",
		Field:      field,
		IsMethod:   true,
		IsResolver: true,
		Child: func(ctx context.Context, field graphql.CollectedField) (*graphql.FieldContext, error) {
			switch field.Name {
			case "id":
				return ec.fieldContext_Notification_id(ctx, field)
			case "kind":
				return ec.fieldContext_Notification_kind(ctx, field)
			case "title":
				return ec.fieldContext_Notification_title(ctx, field)
			case "body":
				return ec.fieldContext_Notification_body(ctx, field)
			case "link":
				return ec.fieldContext_Notification_link(ctx, field)
			case "read":
				return ec.fieldContext_Notification_read(ctx, field)
			case "createdAt":
				return ec.fieldContext_Notification_createdAt(ctx, field)
			}
			return nil, fmt.Errorf("no field named %q was found under type Notification", field.Name)
		},
	}
	return fc, nil
}
