package broker

import (
	"path"
	"time"

	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p-core/peer"
)

const (
	epochsPerDay uint64 = 60 * 24 * 2 // 1 epoch = ~30s
	// MinDealDuration is the minimum allowed deal duration in epochs requested of miners.
	MinDealDuration = epochsPerDay * 365 / 2 // ~6 months
	// MaxDealDuration is the maximum allowed deal duration in epochs requested of miners.
	MaxDealDuration = epochsPerDay * 365 // ~1 year
	// MinDealReplication is the minimum allowed deal replication requested of miners.
	MinDealReplication = 1
	// MaxDealReplication is the maximum allowed deal replication requested of miners.
	MaxDealReplication = 10
	// DefaultDealDeadline is the default deadline for deals.
	DefaultDealDeadline = time.Hour * 48
)

// AuctionID is a unique identifier for an Auction.
type AuctionID string

// Auction defines the core auction model.
type Auction struct {
	ID               AuctionID
	StorageDealID    StorageDealID
	PayloadCid       cid.Cid
	DealSize         uint64
	DealDuration     uint64
	DealReplication  uint32
	DealVerified     bool
	ExcludedMiners   []string
	FilEpochDeadline uint64
	Sources          Sources
	Status           AuctionStatus
	Bids             map[BidID]Bid
	WinningBids      map[BidID]WinningBid
	StartedAt        time.Time
	UpdatedAt        time.Time
	Duration         time.Duration
	Attempts         uint32
	ErrorCause       string
	// Ugly trick: a workaround to avoid calling Auctioneer.finalizeAuction
	// twice, because auctions are enqueued to the Queue again indirectly
	// by Auctioneer.DeliverProposal.
	BrokerAlreadyNotifiedByClosedAuction bool
}

// AuctionStatus is the status of an auction.
type AuctionStatus int

const (
	// AuctionStatusUnspecified indicates the initial or invalid status of an auction.
	AuctionStatusUnspecified AuctionStatus = iota
	// AuctionStatusQueued indicates the auction is currently queued.
	AuctionStatusQueued
	// AuctionStatusStarted indicates the auction has started.
	AuctionStatusStarted
	// AuctionStatusFinalized indicates the auction has reached a final state.
	// If ErrorCause is empty, the auction has received a sufficient number of bids.
	// If ErrorCause is not empty, a fatal error has occurred and the auction should be considered abandoned.
	AuctionStatusFinalized
)

// String returns a string-encoded status.
func (as AuctionStatus) String() string {
	switch as {
	case AuctionStatusUnspecified:
		return "unspecified"
	case AuctionStatusQueued:
		return "queued"
	case AuctionStatusStarted:
		return "started"
	case AuctionStatusFinalized:
		return "finalized"
	default:
		return invalidStatus
	}
}

// BidID is a unique identifier for a Bid.
type BidID string

// Bid defines the core bid model.
type Bid struct {
	MinerAddr        string
	WalletAddrSig    []byte
	BidderID         peer.ID
	AskPrice         int64 // attoFIL per GiB per epoch
	VerifiedAskPrice int64 // attoFIL per GiB per epoch
	StartEpoch       uint64
	FastRetrieval    bool
	ReceivedAt       time.Time
}

// WinningBid contains details about a winning bid.
type WinningBid struct {
	BidderID                peer.ID
	Acknowledged            bool // Whether or not the bidder acknowledged receipt of the win
	ProposalCid             cid.Cid
	ProposalCidAcknowledged bool // Whether or not the bidder acknowledged receipt of the proposal Cid
}

// AuctionTopic is used by brokers to publish and by miners to subscribe to deal auctions.
const AuctionTopic string = "/textile/auction/0.0.1"

// BidsTopic is used by miners to submit deal auction bids.
// "/textile/auction/0.0.1/<auction_id>/bids".
func BidsTopic(auctionID AuctionID) string {
	return path.Join(AuctionTopic, string(auctionID), "bids")
}

// WinsTopic is used by brokers to notify a bidbot that it has won the deal auction.
// "/textile/auction/0.0.1/<peer_id>/wins".
func WinsTopic(pid peer.ID) string {
	return path.Join(AuctionTopic, pid.String(), "wins")
}

// ProposalsTopic is used by brokers to notify a bidbot of the proposal cid.Cid for an accepted deal auction.
// "/textile/auction/0.0.1/<peer_id>/proposals".
func ProposalsTopic(pid peer.ID) string {
	return path.Join(AuctionTopic, pid.String(), "proposals")
}
