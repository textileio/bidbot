package auction

import (
	"path"

	"github.com/libp2p/go-libp2p-core/peer"
)

// ID is a unique identifier for an Auction.
type ID string

// Topic is used by brokers to publish and by miners to subscribe to deal auction.
const Topic string = "/textile/auction/0.0.1"

// BidsTopic is used by miners to submit deal auction bids.
// "/textile/auction/0.0.1/<auction_id>/bids".
func BidsTopic(auctionID ID) string {
	return path.Join(Topic, string(auctionID), "bids")
}

// WinsTopic is used by brokers to notify a bidbot that it has won the deal auction.
// "/textile/auction/0.0.1/<peer_id>/wins".
func WinsTopic(pid peer.ID) string {
	return path.Join(Topic, pid.String(), "wins")
}

// ProposalsTopic is used by brokers to notify a bidbot of the proposal cid.Cid for an accepted deal auction.
// "/textile/auction/0.0.1/<peer_id>/proposals".
func ProposalsTopic(pid peer.ID) string {
	return path.Join(Topic, pid.String(), "proposals")
}
