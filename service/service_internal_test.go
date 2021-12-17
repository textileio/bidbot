package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	pb "github.com/textileio/bidbot/gen/v1"
	core "github.com/textileio/bidbot/lib/auction"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestClientFilter(t *testing.T) {
	t.Parallel()

	// Start with no client filters
	s := Service{
		auctionFilters: AuctionFilters{
			DealDuration: MinMaxFilter{
				Min: core.MinDealDuration,
				Max: core.MaxDealDuration,
			},
			DealSize: MinMaxFilter{
				Min: 1,
				Max: 10,
			},
		},
	}
	auction := &pb.Auction{
		DealSize:      5,
		DealDuration:  core.MinDealDuration,
		EndsAt:        timestamppb.New(time.Now().Add(time.Minute)),
		ClientAddress: "f2kb4izxsxu2jyyslzwmv2sfbrgpld56efedgru5i",
	}

	t.Run("accept-any-client-address", func(t *testing.T) {
		require.Empty(t, s.filterAuction(auction))
	})

	s.auctionFilters.ClientAddressWhitelist = []string{"f144zep4gitj73rrujd3jw6iprljicx6vl4wbeavi"}
	t.Run("reject-single-client-address", func(t *testing.T) {
		require.Contains(t, s.filterAuction(auction), "f2kb4izxsxu2jyyslzwmv2sfbrgpld56efedgru5i")
	})

	auction.ClientAddress = s.auctionFilters.ClientAddressWhitelist[0]
	t.Run("accept-single-client-address", func(t *testing.T) {
		require.Empty(t, s.filterAuction(auction))
	})
}
