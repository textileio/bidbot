package auction

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/multiformats/go-multiaddr"
)

const (
	invalidStatus = "invalid"
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

// StorageDealID is the type of a StorageDeal identifier.
type StorageDealID string

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
	// twice, because auction are enqueued to the Queue again indirectly
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

// CARURL contains details of a CAR file stored in an HTTP endpoint.
type CARURL struct {
	URL url.URL
}

// CARIPFS contains details of a CAR file Cid stored in an HTTP endpoint.
type CARIPFS struct {
	Cid        cid.Cid
	Multiaddrs []multiaddr.Multiaddr
}

// Sources contains information about download sources for prepared data.
type Sources struct {
	CARURL  *CARURL
	CARIPFS *CARIPFS
}

// Validate ensures Sources are valid.
func (s Sources) Validate() error {
	if s.CARURL == nil && s.CARIPFS == nil {
		return errors.New("should contain at least one source")
	}
	if s.CARURL != nil {
		switch s.CARURL.URL.Scheme {
		case "http", "https":
		default:
			return fmt.Errorf("unsupported scheme %s", s.CARURL.URL.Scheme)
		}
	}
	if s.CARIPFS != nil {
		if !s.CARIPFS.Cid.Defined() {
			return errors.New("cid undefined")
		}
		if len(s.CARIPFS.Multiaddrs) == 0 {
			return errors.New("no multiaddr")
		}
	}
	return nil
}

// String returns the string representation of the sources.
func (s Sources) String() string {
	var b strings.Builder
	_, _ = b.WriteString("{")
	if s.CARURL != nil {
		fmt.Fprintf(&b, "url: %s,", s.CARURL.URL.String())
	}
	if s.CARIPFS != nil {
		fmt.Fprintf(&b, "cid: %s,", s.CARIPFS.Cid.String())
	}
	_, _ = b.WriteString("}")
	return b.String()
}
