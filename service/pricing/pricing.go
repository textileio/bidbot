package pricing

import pb "github.com/textileio/bidbot/gen/v1"

// ResolvedPrices represents the result after matching an auction through the pricing rules.
type ResolvedPrices struct {
	AllowBidding         bool
	UnverifiedPriceValid bool
	UnverifiedPrice      int64
	VerifiedPriceValid   bool
	VerifiedPrice        int64
}

// PricingRules represents arbitrary logic to determine the prices for an auction.
type PricingRules interface {
	PricesFor(auction *pb.Auction) (prices ResolvedPrices, valid bool)
}

// EmptyRules is an implementation of PricingRules which always return an invalid result.
type EmptyRules struct{}

// PricesFor always returns valid = false.
func (EmptyRules) PricesFor(auction *pb.Auction) (prices ResolvedPrices, valid bool) {
	return
}
