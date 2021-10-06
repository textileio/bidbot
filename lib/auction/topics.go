package auction

import (
	"path"

	"github.com/libp2p/go-libp2p-core/peer"
)

// ID is a unique identifier for an Auction.
type ID string

// Topic is used by auctioneers to publish and by miners to subscribe to deal auction.
const Topic string = "/textile/auction/0.0.1"

// BidbotEventsTopic is used by bidbots to notify auctioneers various events, mainly around the lifecycle of bids.
var BidbotEventsTopic string = path.Join(Topic, "bidbot_events")

// BidsTopic is used by miners to submit deal auction bids.
// "/textile/auction/0.0.1/<auction_id>/bids".
func BidsTopic(auctionID ID) string {
	return path.Join(Topic, string(auctionID), "bids")
}

// WinsTopic is used by auctioneers to notify a bidbot that it has won the deal auction.
// "/textile/auction/0.0.1/<peer_id>/wins".
func WinsTopic(pid peer.ID) string {
	return path.Join(Topic, pid.String(), "wins")
}

// ProposalsTopic is used by auctioneers to notify a bidbot of the proposal cid.Cid for an accepted deal auction.
// "/textile/auction/0.0.1/<peer_id>/proposals".
func ProposalsTopic(pid peer.ID) string {
	return path.Join(Topic, pid.String(), "proposals")
}
