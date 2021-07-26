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
	epochsPerDay uint64 = 60 * 24 * 2 // 1 epoch = ~30s
	// MinDealDuration is the minimum allowed deal duration in epochs requested of miners.
	MinDealDuration = epochsPerDay * 365 / 2 // ~6 months
	// MaxDealDuration is the maximum allowed deal duration in epochs requested of miners.
	MaxDealDuration = epochsPerDay * 510 // As far as we know, is the safest max duration that all miners accept.

	// HTTPCarHeaderOnly is a HTTP header indicating that the bidbot wants
	// only the CAR file header, as a hint to the HTTP server.
	HTTPCarHeaderOnly = "X-Bidbot-Car-Header-Only"
)

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
