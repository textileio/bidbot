package service

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/oklog/ulid/v2"
	golog "github.com/textileio/go-log/v2"
	"google.golang.org/protobuf/proto"

	pb "github.com/textileio/bidbot/gen/proto/v1/message"
	"github.com/textileio/bidbot/lib/auction"
	"github.com/textileio/bidbot/lib/cast"
	"github.com/textileio/bidbot/lib/dshelper/txndswrap"
	"github.com/textileio/bidbot/lib/filclient"
	"github.com/textileio/bidbot/lib/finalizer"
	"github.com/textileio/bidbot/lib/lotusclient"
	"github.com/textileio/bidbot/lib/marketpeer"

	"github.com/textileio/bidbot/service/datauri"
	"github.com/textileio/bidbot/service/limiter"
	bidstore "github.com/textileio/bidbot/service/store"
)

var (
	log = golog.Logger("bidbot/service")

	// bidsAckTimeout is the max duration bidbot will wait for an ack after bidding in an auction.
	bidsAckTimeout = time.Second * 10

	// dataURIValidateTimeout is the timeout used when validating a data uri.
	dataURIValidateTimeout = time.Minute
)

// Config defines params for Service configuration.
type Config struct {
	Peer           marketpeer.Config
	BidParams      BidParams
	AuctionFilters AuctionFilters
	BytesLimiter   limiter.Limiter
}

// BidParams defines how bids are made.
type BidParams struct {
	// MinerAddr is your Filecoin miner address used to make deals.
	MinerAddr string
	// WalletAddrSig is a signature from your owner Lotus wallet address used to authenticate bids.
	WalletAddrSig []byte

	// AskPrice in attoFIL per GiB per epoch.
	AskPrice int64
	// VerifiedAskPrice in attoFIL per GiB per epoch.
	VerifiedAskPrice int64
	// FastRetrieval is whether or not you're offering fast retrieval for the deal data.
	FastRetrieval bool
	// DealStartWindow is the number of epochs after which won deals must start be on-chain.
	DealStartWindow uint64

	// DealDataDirectory is the directory to which deal data will be written.
	DealDataDirectory string
	// DealDataFetchAttempts is the number of times fetching deal data cid will be attempted.
	DealDataFetchAttempts uint32
	// DiscardOrphanDealsAfter is the time after which deals with no progress will be removed.
	DiscardOrphanDealsAfter time.Duration
}

// Validate ensures BidParams are valid.
func (p *BidParams) Validate() error {
	if p.DealStartWindow == 0 {
		return fmt.Errorf("invalid deal start window; must be greater than zero")
	}
	if p.DealDataFetchAttempts == 0 {
		return fmt.Errorf("invalid deal data fetch attempts; must be greater than zero")
	}
	if p.DealDataDirectory == "" {
		return fmt.Errorf("invalid deal data directory; must not be empty")
	}
	if err := os.MkdirAll(p.DealDataDirectory, os.ModePerm); err != nil {
		return fmt.Errorf("initializing data directory: %v", err)
	}
	testFile := filepath.Join(p.DealDataDirectory, ulid.MustNew(ulid.Now(), rand.Reader).String())
	if err := ioutil.WriteFile(testFile, []byte("testing"), 0644); err != nil {
		return fmt.Errorf("checking write access to data directory: %v", err)
	}
	if err := os.Remove(testFile); err != nil {
		return fmt.Errorf("removing data directory write test file: %v", err)
	}
	return nil
}

// AuctionFilters specifies filters used when selecting auction to bid on.
type AuctionFilters struct {
	// DealDuration sets the min and max deal duration to bid on.
	DealDuration MinMaxFilter
	// DealSize sets the min and max deal size to bid on.
	DealSize MinMaxFilter
}

// Validate ensures AuctionFilters are valid.
func (f *AuctionFilters) Validate() error {
	if err := f.DealDuration.Validate(); err != nil {
		return fmt.Errorf("invalid deal duration filter: %v", err)
	}
	if err := f.DealDuration.Validate(); err != nil {
		return fmt.Errorf("invalid deal size filter: %v", err)
	}
	return nil
}

// MinMaxFilter is used to specify a range for an auction filter.
type MinMaxFilter struct {
	Min uint64
	Max uint64
}

// Validate ensures the filter is a valid min max window.
func (f *MinMaxFilter) Validate() error {
	if f.Min > f.Max {
		return errors.New("min must be less than or equal to max")
	}
	return nil
}

// Service is a miner service that subscribes to brokered deals.
type Service struct {
	peer       *marketpeer.Peer
	fc         filclient.FilClient
	store      *bidstore.Store
	subscribed bool

	bidParams      BidParams
	auctionFilters AuctionFilters
	bytesLimiter   limiter.Limiter

	ctx       context.Context
	finalizer *finalizer.Finalizer
	lk        sync.Mutex
}

// New returns a new Service.
func New(
	conf Config,
	store txndswrap.TxnDatastore,
	lc lotusclient.LotusClient,
	fc filclient.FilClient,
) (*Service, error) {
	if err := conf.BidParams.Validate(); err != nil {
		return nil, fmt.Errorf("validating bid parameters: %v", err)
	}
	if err := conf.AuctionFilters.Validate(); err != nil {
		return nil, fmt.Errorf("validating auction filters: %v", err)
	}
	fin := finalizer.NewFinalizer()
	ctx, cancel := context.WithCancel(context.Background())
	fin.Add(finalizer.NewContextCloser(cancel))

	// Create miner peer
	p, err := marketpeer.New(conf.Peer)
	if err != nil {
		return nil, fin.Cleanupf("creating peer: %v", err)
	}
	fin.Add(p)

	bytesLimiter := limiter.NopeLimiter{}
	// Create bid store
	s, err := bidstore.NewStore(
		store,
		p.Host(),
		p.DAGService(),
		lc,
		conf.BidParams.DealDataDirectory,
		conf.BidParams.DealDataFetchAttempts,
		conf.BidParams.DiscardOrphanDealsAfter,
		bytesLimiter,
	)
	if err != nil {
		return nil, fin.Cleanupf("creating bid store: %v", err)
	}
	fin.Add(s)

	// Verify miner address
	ok, err := fc.VerifyBidder(
		conf.BidParams.WalletAddrSig,
		p.Host().ID(),
		conf.BidParams.MinerAddr)
	if err != nil {
		return nil, fin.Cleanupf("verifying miner address: %v", err)
	}
	if !ok {
		return nil, fin.Cleanup(fmt.Errorf("invalid miner address or signature"))
	}

	srv := &Service{
		peer:           p,
		fc:             fc,
		store:          s,
		bidParams:      conf.BidParams,
		auctionFilters: conf.AuctionFilters,
		bytesLimiter:   bytesLimiter,
		ctx:            ctx,
		finalizer:      fin,
	}
	log.Info("service started")

	return srv, nil
}

// Close the service.
func (s *Service) Close() error {
	log.Info("service was shutdown")
	return s.finalizer.Cleanup(nil)
}

// Subscribe to the deal auction feed.
// If bootstrap is true, the peer will dial the configured bootstrap addresses
// before joining the deal auction feed.
func (s *Service) Subscribe(bootstrap bool) error {
	s.lk.Lock()
	defer s.lk.Unlock()
	if s.subscribed {
		return nil
	}

	// Bootstrap against configured addresses
	if bootstrap {
		s.peer.Bootstrap()
	}

	// Subscribe to the global auction topic
	a, err := s.peer.NewTopic(s.ctx, auction.AuctionTopic, true)
	if err != nil {
		return fmt.Errorf("creating auction topic: %v", err)
	}
	a.SetEventHandler(s.eventHandler)
	a.SetMessageHandler(s.auctionHandler)

	// Subscribe to our own wins topic
	wins, err := s.peer.NewTopic(s.ctx, auction.WinsTopic(s.peer.Host().ID()), true)
	if err != nil {
		if err := a.Close(); err != nil {
			log.Errorf("closing auction topic: %v", err)
		}
		return fmt.Errorf("creating wins topic: %v", err)
	}
	wins.SetEventHandler(s.eventHandler)
	wins.SetMessageHandler(s.winsHandler)

	// Subscribe to our own proposals topic
	props, err := s.peer.NewTopic(s.ctx, auction.ProposalsTopic(s.peer.Host().ID()), true)
	if err != nil {
		if err := a.Close(); err != nil {
			log.Errorf("closing auction topic: %v", err)
		}
		if err := wins.Close(); err != nil {
			log.Errorf("closing wins topic: %v", err)
		}
		return fmt.Errorf("creating proposals topic: %v", err)
	}
	props.SetEventHandler(s.eventHandler)
	props.SetMessageHandler(s.proposalHandler)

	s.finalizer.Add(a, wins, props)

	log.Info("subscribed to the deal auction feed")
	s.subscribed = true

	s.finalizer.AddFn(s.printStats())
	return nil
}

// PeerInfo returns the public information of the market peer.
func (s *Service) PeerInfo() (*marketpeer.PeerInfo, error) {
	return s.peer.Info()
}

// ListBids lists bids by applying a store.Query.
func (s *Service) ListBids(query bidstore.Query) ([]*bidstore.Bid, error) {
	return s.store.ListBids(query)
}

// GetBid gets the bid with specific ID.
func (s *Service) GetBid(id auction.BidID) (*bidstore.Bid, error) {
	return s.store.GetBid(id)
}

// WriteDataURI writes a data uri resource to the configured deal data directory.
func (s *Service) WriteDataURI(payloadCid, uri string) (string, error) {
	return s.store.WriteDataURI(auction.BidID(""), payloadCid, uri)
}

func (s *Service) eventHandler(from peer.ID, topic string, msg []byte) {
	log.Debugf("%s peer event: %s %s", topic, from, msg)
}

func (s *Service) auctionHandler(from peer.ID, topic string, msg []byte) ([]byte, error) {
	log.Debugf("%s received auction from %s", topic, from)

	auction := &pb.Auction{}
	if err := proto.Unmarshal(msg, auction); err != nil {
		return nil, fmt.Errorf("unmarshaling message: %v", err)
	}

	auctionj, err := json.MarshalIndent(auction, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling json: %v", err)
	}
	log.Infof("received auction %s from %s: \n%s", auction.Id, from, string(auctionj))

	go func() {
		if err := s.makeBid(auction, from); err != nil {
			log.Errorf("making bid: %v", err)
		}
	}()
	return nil, nil
}

func (s *Service) winsHandler(from peer.ID, topic string, msg []byte) ([]byte, error) {
	log.Debugf("%s received win from %s", topic, from)

	win := &pb.WinningBid{}
	if err := proto.Unmarshal(msg, win); err != nil {
		return nil, fmt.Errorf("unmarshaling message: %v", err)
	}

	if err := s.store.SetAwaitingProposalCid(auction.BidID(win.BidId)); err != nil {
		return nil, fmt.Errorf("setting awaiting proposal cid: %v", err)
	}

	log.Infof("bid %s won in auction %s; awaiting proposal cid", win.BidId, win.AuctionId)
	return nil, nil
}

func (s *Service) proposalHandler(from peer.ID, topic string, msg []byte) ([]byte, error) {
	log.Debugf("%s received proposal from %s", topic, from)

	prop := &pb.WinningBidProposal{}
	if err := proto.Unmarshal(msg, prop); err != nil {
		return nil, fmt.Errorf("unmarshaling message: %v", err)
	}

	pcid, err := cid.Decode(prop.ProposalCid)
	if err != nil {
		return nil, fmt.Errorf("decoding proposal cid: %v", err)
	}
	if err := s.store.SetProposalCid(auction.BidID(prop.BidId), pcid); err != nil {
		return nil, fmt.Errorf("setting proposal cid: %v", err)
	}

	log.Infof("bid %s received proposal cid %s in auction %s", prop.BidId, prop.ProposalCid, prop.AuctionId)
	return nil, nil
}

func (s *Service) makeBid(a *pb.Auction, from peer.ID) error {
	if rejectReason := s.filterAuction(a); rejectReason != "" {
		log.Infof("not bidding in auction %s from %s: %s", a.Id, from, rejectReason)
		return nil
	}

	if !s.bytesLimiter.Request(a.DealSize) {
		log.Infof("not bidding in auction %s from %s: would exceed the running total bytes limit", a.Id, from)
		return nil
	}

	sources, err := cast.SourcesFromPb(a.Sources)
	if err != nil {
		return err
	}
	// Ensure we can fetch the data
	dataURI, err := datauri.NewFromSources(a.PayloadCid, sources)
	if err != nil {
		return fmt.Errorf("parsing data uri: %v", err)
	}
	ctx, cancel := context.WithTimeout(s.ctx, dataURIValidateTimeout)
	defer cancel()
	if err := dataURI.Validate(ctx); err != nil {
		return fmt.Errorf("validating data uri: %v", err)
	}

	if err := s.store.HealthCheck(); err != nil {
		return fmt.Errorf("store not ready to bid: %v", err)
	}

	// Get current chain height
	currentEpoch, err := s.fc.GetChainHeight()
	if err != nil {
		return fmt.Errorf("getting chain height: %v", err)
	}
	startEpoch := s.bidParams.DealStartWindow + currentEpoch
	if a.FilEpochDeadline > 0 && a.FilEpochDeadline < startEpoch {
		log.Infof("auction %s from %s requires epoch no later than %d, but I can only promise epoch %d, skip bidding",
			a.Id, from, a.FilEpochDeadline, startEpoch)
		return nil
	}

	// Create bids topic
	topic, err := s.peer.NewTopic(s.ctx, auction.BidsTopic(auction.AuctionID(a.Id)), false)
	if err != nil {
		return fmt.Errorf("creating bids topic: %v", err)
	}
	defer func() {
		if err := topic.Close(); err != nil {
			log.Errorf("closing bids topic: %v", err)
		}
	}()
	topic.SetEventHandler(s.eventHandler)

	// Submit bid to auctioneer
	bid := &pb.Bid{
		AuctionId:        a.Id,
		MinerAddr:        s.bidParams.MinerAddr,
		WalletAddrSig:    s.bidParams.WalletAddrSig,
		AskPrice:         s.bidParams.AskPrice,
		VerifiedAskPrice: s.bidParams.VerifiedAskPrice,
		StartEpoch:       startEpoch,
		FastRetrieval:    s.bidParams.FastRetrieval,
	}
	bidj, err := json.MarshalIndent(bid, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling json: %v", err)
	}
	log.Infof("bidding in auction %s from %s: \n%s", a.Id, from, string(bidj))

	msg, err := proto.Marshal(bid)
	if err != nil {
		return fmt.Errorf("marshaling message: %v", err)
	}

	ctx2, cancel2 := context.WithTimeout(s.ctx, bidsAckTimeout)
	defer cancel2()
	res, err := topic.Publish(ctx2, msg)
	if err != nil {
		return fmt.Errorf("publishing bid: %v", err)
	}
	r := <-res
	if r.Err != nil {
		return fmt.Errorf("publishing bid; auctioneer %s returned error: %v", from, r.Err)
	}
	id := r.Data

	payloadCid, err := cid.Parse(a.PayloadCid)
	if err != nil {
		return fmt.Errorf("parsing payload cid: %v", err)
	}
	// Save bid locally
	if err := s.store.SaveBid(bidstore.Bid{
		ID:               auction.BidID(id),
		AuctionID:        auction.AuctionID(a.Id),
		AuctioneerID:     from,
		PayloadCid:       payloadCid,
		DealSize:         a.DealSize,
		DealDuration:     a.DealDuration,
		Sources:          sources,
		AskPrice:         bid.AskPrice,
		VerifiedAskPrice: bid.VerifiedAskPrice,
		StartEpoch:       bid.StartEpoch,
		FastRetrieval:    bid.FastRetrieval,
	}); err != nil {
		return fmt.Errorf("saving bid: %v", err)
	}

	log.Debugf("created bid %s in auction %s", id, a.Id)
	return nil
}

func (s *Service) filterAuction(auction *pb.Auction) (rejectReason string) {
	if !auction.EndsAt.IsValid() || auction.EndsAt.AsTime().Before(time.Now()) {
		return "auction ended or has an invalid end time"
	}

	if auction.DealSize < s.auctionFilters.DealSize.Min ||
		auction.DealSize > s.auctionFilters.DealSize.Max {
		return fmt.Sprintf("deal size falls outside of the range [%d, %d]",
			s.auctionFilters.DealSize.Min,
			s.auctionFilters.DealSize.Max)
	}

	if auction.DealDuration < s.auctionFilters.DealDuration.Min ||
		auction.DealDuration > s.auctionFilters.DealDuration.Max {
		return fmt.Sprintf("deal duration falls outside of the range [%d, %d]",
			s.auctionFilters.DealDuration.Min,
			s.auctionFilters.DealDuration.Max)
	}

	return ""
}

func (s *Service) printStats() func() {
	startAt := time.Now()
	tk := time.NewTicker(10 * time.Minute)
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-tk.C:
				if err := s.store.HealthCheck(); err != nil {
					log.Errorf("store not healthy: %v", err)
				}
				log.Infof("bidbot up %v, %d connected peers",
					time.Since(startAt), len(s.peer.ListPeers()))
			}
		}
	}()
	return func() { close(stop) }
}
