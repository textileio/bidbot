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
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/ipfs/go-cid"
	"github.com/libp2p/go-libp2p-core/crypto"
	core "github.com/libp2p/go-libp2p-core/peer"
	"github.com/oklog/ulid/v2"
	"github.com/textileio/bidbot/buildinfo"
	pb "github.com/textileio/bidbot/gen/v1"
	"github.com/textileio/bidbot/lib/auction"
	"github.com/textileio/bidbot/lib/cast"
	"github.com/textileio/bidbot/lib/datauri"
	"github.com/textileio/bidbot/lib/dshelper/txndswrap"
	"github.com/textileio/bidbot/lib/filclient"
	"github.com/textileio/bidbot/service/limiter"
	"github.com/textileio/bidbot/service/lotusclient"
	"github.com/textileio/bidbot/service/pricing"
	bidstore "github.com/textileio/bidbot/service/store"
	tcrypto "github.com/textileio/crypto"
	"github.com/textileio/go-libp2p-pubsub-rpc/finalizer"
	"github.com/textileio/go-libp2p-pubsub-rpc/peer"
	golog "github.com/textileio/go-log/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	log = golog.Logger("bidbot/service")

	// bidsAckTimeout is the max duration bidbot will wait for an ack after bidding in an auction.
	bidsAckTimeout = time.Second * 30

	// BidsExpiration is the duration to wait for a proposal CID after
	// which bidbot will consider itself not winning in an auction, so the
	// resources can be freed up.
	BidsExpiration = 10 * time.Minute

	// dataURIValidateTimeout is the timeout used when validating a data uri.
	dataURIValidateTimeout = time.Minute

	errWouldExceedRunningBytesLimit = errors.New(auction.ErrStringWouldExceedRunningBytesLimit)
)

// Config defines params for Service configuration.
type Config struct {
	Peer                peer.Config
	BidParams           BidParams
	AuctionFilters      AuctionFilters
	BytesLimiter        limiter.Limiter
	ConcurrentImports   int
	ChunkedDownload     bool
	SealingSectorsLimit int
	PricingRules        pricing.PricingRules
	PricingRulesStrict  bool
}

// BidParams defines how bids are made.
type BidParams struct {
	// StorageProviderID is your Filecoin StorageProvider ID used to make deals.
	StorageProviderID string
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
	// DealDataFetchTimeout is the timeout fetching deal data cid.
	DealDataFetchTimeout time.Duration
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

// AuctionFilters specifies filters used when selecting auctions to bid on.
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

// Service is a miner service that subscribes to auctions.
type Service struct {
	commChannel CommChannel
	decryptKey  tcrypto.DecryptionKey
	fc          filclient.FilClient
	lc          lotusclient.LotusClient
	store       *bidstore.Store

	paused              int32 // atomic. 1 == paused; 0 == not paused
	bidParams           BidParams
	auctionFilters      AuctionFilters
	bytesLimiter        limiter.Limiter
	sealingSectorsLimit int
	pricingRules        pricing.PricingRules
	pricingRulesStrict  bool

	ctx       context.Context
	finalizer *finalizer.Finalizer
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

	commChannel, err := NewLibp2pPubsub(ctx, conf.Peer)
	if err != nil {
		return nil, fin.Cleanupf("creating peer: %v", err)
	}
	fin.Add(commChannel)

	isRunningBoost, err := lc.IsRunningBoost()
	if err != nil {
		return nil, fin.Cleanupf("detecting if storage-provider is running Boost: %s", err)
	}
	log.Infof("running-boost %v", isRunningBoost)

	// Create bid store
	s, err := bidstore.NewStore(
		store,
		fc,
		lc,
		conf.BidParams.DealDataDirectory,
		conf.BidParams.DealDataFetchAttempts,
		conf.BidParams.DealDataFetchTimeout,
		progressReporter{commChannel, ctx},
		conf.BidParams.DiscardOrphanDealsAfter,
		conf.BytesLimiter,
		conf.ConcurrentImports,
		conf.ChunkedDownload,
	)
	if err != nil {
		return nil, fin.Cleanupf("creating bid store: %v", err)
	}
	fin.Add(s)

	// Verify StorageProvider ID
	ok, err := fc.VerifyBidder(
		conf.BidParams.WalletAddrSig,
		commChannel.ID(),
		conf.BidParams.StorageProviderID)
	if err != nil {
		return nil, fin.Cleanupf("verifying StorageProvider ID: %v", err)
	}
	if !ok {
		return nil, fin.Cleanup(fmt.Errorf("invalid StorageProvider ID or signature"))
	}

	privKey, err := crypto.MarshalPrivateKey(conf.Peer.PrivKey)
	if err != nil {
		return nil, fin.Cleanupf("marshaling private key: %v", err)
	}
	decryptKey, err := tcrypto.DecryptionKeyFromBytes(privKey)
	if err != nil {
		return nil, fin.Cleanupf("creating decryption key: %v", err)
	}

	srv := &Service{
		commChannel:         commChannel,
		decryptKey:          decryptKey,
		fc:                  fc,
		lc:                  lc,
		store:               s,
		bidParams:           conf.BidParams,
		auctionFilters:      conf.AuctionFilters,
		bytesLimiter:        conf.BytesLimiter,
		sealingSectorsLimit: conf.SealingSectorsLimit,
		pricingRules:        conf.PricingRules,
		pricingRulesStrict:  conf.PricingRulesStrict,
		ctx:                 ctx,
		finalizer:           fin,
	}
	if srv.pricingRules == nil {
		srv.pricingRules = pricing.EmptyRules{}
	}
	srv.finalizer.AddFn(srv.healthChecks())
	log.Info("service started")

	return srv, nil
}

// Close the service.
func (s *Service) Close() error {
	log.Info("service was shutdown")
	return s.finalizer.Cleanup(nil)
}

// Subscribe to the deal auction feed. Upon success, it reports basic bidbot information to auctioneer after some delay.
// If bootstrap is true, the peer will dial the configured bootstrap addresses before joining the deal auction feed.
func (s *Service) Subscribe(bootstrap bool) error {
	err := s.commChannel.Subscribe(bootstrap, s)
	if err == nil {
		time.AfterFunc(30*time.Second, s.reportStartup)
	}
	return err
}

// PeerInfo returns the public information of the market peer.
func (s *Service) PeerInfo() (*peer.Info, error) {
	return s.commChannel.Info()
}

// ListBids lists bids by applying a store.Query.
func (s *Service) ListBids(query bidstore.Query) ([]*bidstore.Bid, error) {
	return s.store.ListBids(query)
}

// GetBid gets the bid with specific ID.
func (s *Service) GetBid(ctx context.Context, id auction.BidID) (*bidstore.Bid, error) {
	return s.store.GetBid(ctx, id)
}

// WriteDataURI writes a data uri resource to the configured deal data directory.
func (s *Service) WriteDataURI(payloadCid, uri string) (string, error) {
	return s.store.WriteDataURI("", payloadCid, uri, 0)
}

// SetPaused sets the service state to pause bidding or not.
func (s *Service) SetPaused(paused bool) {
	var v int32
	if paused {
		v = 1
	}
	atomic.StoreInt32(&s.paused, v)
}

func (s *Service) isPaused() bool {
	return atomic.LoadInt32(&s.paused) == 1
}

// AuctionsHandler implements MessageHandler.
func (s *Service) AuctionsHandler(from core.ID, a *pb.Auction) error {
	if s.isPaused() {
		log.Info("not bidding when bidbot is paused")
		return nil
	}
	ajson, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling json: %v", err)
	}
	log.Infof("auction details:\n%s", string(ajson))
	go func() {
		ctx, cls := context.WithTimeout(context.Background(), time.Second*30)
		defer cls()
		if err := s.makeBid(ctx, a, from); err != nil {
			log.Errorf("making bid: %v", err)
		}
	}()
	return nil
}

func (s *Service) makeBid(ctx context.Context, a *pb.Auction, from core.ID) error {
	if rejectReason := s.filterAuction(a); rejectReason != "" {
		log.Infof("not bidding in auction %s from %s: %s", a.Id, from, rejectReason)
		return nil
	}

	if s.sealingSectorsLimit > 0 {
		n, err := s.lc.CurrentSealingSectors()
		if err != nil {
			log.Errorf("fail to get number of sealing sectors, continuing: %v", err)
		} else if n > s.sealingSectorsLimit {
			log.Infof("not bidding: lotus already has %d sealing sectors", n)
			return nil
		}
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

	prices, valid := s.pricingRules.PricesFor(a)
	log.Infof("pricing engine result valid for auction %s?: %v, details: %+v", a.Id, valid, prices)
	if !valid {
		// fail to load rules, allow bidding unless pricingRulesStrict is set.
		if s.pricingRulesStrict {
			return nil
		}
		prices.AllowBidding = true
	}
	if !prices.AllowBidding {
		return nil
	}
	if !prices.UnverifiedPriceValid {
		prices.UnverifiedPrice = s.bidParams.AskPrice
	}
	if !prices.VerifiedPriceValid {
		prices.VerifiedPrice = s.bidParams.VerifiedAskPrice
	}

	bid := &pb.Bid{
		AuctionId:         a.Id,
		StorageProviderId: s.bidParams.StorageProviderID,
		WalletAddrSig:     []byte("***"),
		AskPrice:          prices.UnverifiedPrice,
		VerifiedAskPrice:  prices.VerifiedPrice,
		StartEpoch:        startEpoch,
		FastRetrieval:     s.bidParams.FastRetrieval,
	}
	bidj, err := json.MarshalIndent(bid, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling json: %v", err)
	}
	log.Infof("bidding in auction %s from %s: \n%s", a.Id, from, string(bidj))

	bid.WalletAddrSig = s.bidParams.WalletAddrSig
	// Submit bid to auctioneer
	ctx2, cancel2 := context.WithTimeout(s.ctx, bidsAckTimeout)
	defer cancel2()

	id, err := s.commChannel.PublishBid(ctx2, auction.BidsTopic(auction.ID(a.Id)), bid)
	if err != nil {
		return fmt.Errorf("sending bid: %v", err)
	}

	payloadCid, err := cid.Parse(a.PayloadCid)
	if err != nil {
		return fmt.Errorf("parsing payload cid: %v", err)
	}
	// Save bid locally
	if err := s.store.SaveBid(ctx, bidstore.Bid{
		ID:               auction.BidID(id),
		AuctionID:        auction.ID(a.Id),
		AuctioneerID:     from,
		PayloadCid:       payloadCid,
		ClientAddress:    a.ClientAddress,
		DealSize:         a.DealSize,
		DealDuration:     a.DealDuration,
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

// WinsHandler implements MessageHandler.
func (s *Service) WinsHandler(ctx context.Context, wb *pb.WinningBid) error {
	bid, err := s.store.GetBid(ctx, auction.BidID(wb.BidId))
	if err != nil {
		return fmt.Errorf("getting bid: %v", err)
	}
	// request for some quota, which may be used or gets expired if not winning
	// the auction.
	granted := s.bytesLimiter.Request(wb.AuctionId, bid.DealSize, BidsExpiration)
	if !granted {
		return errWouldExceedRunningBytesLimit
	}

	decrypted, err := s.decryptKey.Decrypt(wb.Encrypted)
	if err != nil {
		return fmt.Errorf("decrypting sources: %v", err)
	}
	confidential := &pb.WinningBidConfidential{}
	if err := proto.Unmarshal(decrypted, confidential); err != nil {
		return fmt.Errorf("unmarshaling sources: %v", err)
	}
	sources, err := cast.SourcesFromPb(confidential.Sources)
	if err != nil {
		return fmt.Errorf("sources from pb: %v", err)
	}
	// Ensure we can fetch the data
	dataURI, err := datauri.NewFromSources(bid.PayloadCid.String(), sources)
	if err != nil {
		return fmt.Errorf("parsing data uri: %v", err)
	}
	ctx, cancel := context.WithTimeout(s.ctx, dataURIValidateTimeout)
	defer cancel()
	if err := dataURI.Validate(ctx); err != nil {
		return fmt.Errorf("validating data uri: %v", err)
	}

	if err := s.store.SetAwaitingProposalCid(ctx, auction.BidID(wb.BidId), sources); err != nil {
		return fmt.Errorf("setting awaiting proposal cid: %v", err)
	}

	log.Infof("bid %s won in auction %s; awaiting proposal cid", wb.BidId, wb.AuctionId)
	return nil
}

// ProposalsHandler implements MessageHandler.
func (s *Service) ProposalsHandler(ctx context.Context, prop *pb.WinningBidProposal) error {
	if _, err := uuid.Parse(prop.DealUid); err != nil {
		log.Info("bid %s received deal uid %s in auction %s", prop.BidId, prop.DealUid, prop.AuctionId)
		if err := s.store.SetDealUID(ctx, auction.BidID(prop.BidId), prop.DealUid); err != nil {
			return fmt.Errorf("setting proposal cid: %v", err)
		}
	}

	log.Infof("bid %s received proposal cid %s in auction %s", prop.BidId, prop.ProposalCid, prop.AuctionId)
	pcid, err := cid.Decode(prop.ProposalCid)
	if err != nil {
		return fmt.Errorf("decoding proposal cid: %v", err)
	}
	if err := s.store.SetProposalCid(ctx, auction.BidID(prop.BidId), pcid); err != nil {
		return fmt.Errorf("setting proposal cid: %v", err)
	}
	// ready to fetch data, so the requested quota is actually in use.
	s.bytesLimiter.Secure(prop.AuctionId)
	return nil
}

func (s *Service) reportStartup() {
	_, unconfigured := s.pricingRules.(pricing.EmptyRules)
	event := &pb.BidbotEvent{
		Ts: timestamppb.New(time.Now()),
		Type: &pb.BidbotEvent_Startup_{Startup: &pb.BidbotEvent_Startup{
			SemanticVersion:      buildinfo.GitSummary,
			DealStartWindow:      s.bidParams.DealStartWindow,
			StorageProviderId:    s.bidParams.StorageProviderID,
			CidGravityConfigured: !unconfigured,
			CidGravityStrict:     s.pricingRulesStrict,
		}},
	}
	s.commChannel.PublishBidbotEvent(s.ctx, event)
}

func (s *Service) healthChecks() func() {
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
					s.reportUnhealthy(err)
				}
			}
		}
	}()
	return func() { close(stop) }
}

func (s *Service) reportUnhealthy(err error) {
	event := &pb.BidbotEvent{
		Ts: timestamppb.New(time.Now()),
		Type: &pb.BidbotEvent_Unhealthy_{Unhealthy: &pb.BidbotEvent_Unhealthy{
			StorageProviderId: s.bidParams.StorageProviderID,
			Error:             err.Error(),
		}},
	}
	s.commChannel.PublishBidbotEvent(s.ctx, event)
}
