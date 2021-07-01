package broker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ipfs/go-cid"
)

const (
	invalidStatus = "invalid"
)

// Broker provides full set of functionalities for Filecoin brokering.
type Broker interface {
	// Create creates a new BrokerRequest for a cid.
	Create(ctx context.Context, dataCid cid.Cid) (BrokerRequest, error)

	// CreatePrepared creates a new BrokerRequest for prepared data.
	CreatePrepared(ctx context.Context, payloadCid cid.Cid, pc PreparedCAR) (BrokerRequest, error)

	// GetBrokerRequestInfo returns a broker request information by id.
	GetBrokerRequestInfo(ctx context.Context, ID BrokerRequestID) (BrokerRequestInfo, error)

	// CreateStorageDeal creates a new StorageDeal. It is called
	// by the Packer after batching a set of BrokerRequest properly.
	CreateStorageDeal(ctx context.Context, batchCid cid.Cid, srids []BrokerRequestID) (StorageDealID, error)

	// StorageDealPrepared signals the broker that a StorageDeal was prepared and it's ready to auction.
	StorageDealPrepared(ctx context.Context, id StorageDealID, pr DataPreparationResult) error

	// StorageDealAuctioned signals to the broker that StorageDeal auction has completed.
	StorageDealAuctioned(ctx context.Context, auction Auction) error

	// StorageDealFinalizedDeal signals to the broker results about deal making.
	StorageDealFinalizedDeal(ctx context.Context, fad FinalizedAuctionDeal) error

	// StorageDealProposalAccepted signals the broker that a miner has accepted a deal proposal.
	StorageDealProposalAccepted(ctx context.Context, sdID StorageDealID, miner string, proposalCid cid.Cid) error
}

// StorageDeal is the underlying entity that gets into bidding and
// store data in the Filecoin network. It groups one or multiple
// BrokerRequests.
type StorageDeal struct {
	ID                 StorageDealID
	Status             StorageDealStatus
	BrokerRequestIDs   []BrokerRequestID
	RepFactor          int
	DealDuration       int
	Sources            Sources
	DisallowRebatching bool
	AuctionRetries     int
	FilEpochDeadline   uint64
	CreatedAt          time.Time
	UpdatedAt          time.Time
	Error              string

	// Packer calculates this field after batching storage requests.
	PayloadCid cid.Cid

	// Piecer calculates these fields after preparing the batched DAG.
	PieceCid  cid.Cid
	PieceSize uint64

	// Dealer populates this field
	Deals []MinerDeal
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

// StorageDealID is the type of a StorageDeal identifier.
type StorageDealID string

// StorageDealStatus is the type of a broker status.
type StorageDealStatus int

const (
	// StorageDealUnkown is an invalid status value. Defined for safety.
	StorageDealUnkown StorageDealStatus = iota
	// StorageDealPreparing indicates that the storage deal is being prepared.
	StorageDealPreparing
	// StorageDealAuctioning indicates that the storage deal is being auctioned.
	StorageDealAuctioning
	// StorageDealDealMaking indicates that the storage deal deals are being executed.
	StorageDealDealMaking
	// StorageDealSuccess indicates that the storage deal was successfully stored in Filecoin.
	StorageDealSuccess
	// StorageDealError indicates that the storage deal has errored.
	StorageDealError
)

// String returns a string-encoded status.
func (sds StorageDealStatus) String() string {
	switch sds {
	case StorageDealUnkown:
		return "unknown"
	case StorageDealPreparing:
		return "preparing"
	case StorageDealAuctioning:
		return "auctioning"
	case StorageDealDealMaking:
		return "deal making"
	case StorageDealSuccess:
		return "success"
	default:
		return invalidStatus
	}
}

// MinerDeal contains information about a miner deal resulted from
// winned auctions:
// If ErrCause is not empty, is a failed deal.
// If ErrCause is empty, and DealID is zero then the deal is in progress.
// IF ErrCause is empty, and DealID is not zero then is final.
type MinerDeal struct {
	StorageDealID StorageDealID
	AuctionID     AuctionID
	BidID         BidID
	CreatedAt     time.Time
	UpdatedAt     time.Time

	Miner          string
	DealID         int64
	DealExpiration uint64
	ErrorCause     string
}

// DataPreparationResult is the result of preparing a StorageDeal.
type DataPreparationResult struct {
	PieceSize uint64
	PieceCid  cid.Cid
}

// Validate returns an error if the struct contain invalid fields.
func (dpr DataPreparationResult) Validate() error {
	if dpr.PieceSize == 0 {
		return errors.New("piece size is zero")
	}
	if !dpr.PieceCid.Defined() {
		return errors.New("piece cid is undefined")
	}
	return nil
}

// FinalizedAuctionDeal contains information about a finalized deal.
type FinalizedAuctionDeal struct {
	StorageDealID  StorageDealID
	Miner          string
	DealID         int64
	DealExpiration uint64
	ErrorCause     string
}
