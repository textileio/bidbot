package store

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ipfs/go-cid"
	ds "github.com/ipfs/go-datastore"
	dsq "github.com/ipfs/go-datastore/query"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/oklog/ulid/v2"
	"github.com/textileio/bidbot/lib/auction"
	"github.com/textileio/bidbot/lib/datauri"
	"github.com/textileio/bidbot/lib/dshelper/txndswrap"
	"github.com/textileio/bidbot/service/limiter"
	"github.com/textileio/bidbot/service/lotusclient"
	dsextensions "github.com/textileio/go-datastore-extensions"
	golog "github.com/textileio/go-log/v2"
	"golang.org/x/sync/semaphore"
)

const (
	// gcInterval specifies how often to run the periodical garbage collector.
	gcInterval = time.Hour
	// defaultListLimit is the default list page size.
	defaultListLimit = 10
	// maxListLimit is the max list page size.
	maxListLimit = 1000
)

var (
	log = golog.Logger("bidbot/store")

	// DataURIFetchStartDelay is the time delay before the store will process queued data uri fetches on start.
	DataURIFetchStartDelay = time.Second * 10

	// MaxDataURIFetchConcurrency is the maximum number of data uri fetches that will be handled concurrently.
	MaxDataURIFetchConcurrency = 3

	// ErrBidNotFound indicates the requested bid was not found.
	ErrBidNotFound = errors.New("bid not found")

	// dsPrefix is the prefix for bids.
	// Structure: /bids/<bid_id> -> Bid.
	dsPrefix = ds.NewKey("/bids")

	// dsQueuedPrefix is the prefix for queued data uri fetches.
	// Structure: /data_queued/<bid_id> -> nil.
	dsQueuedPrefix = ds.NewKey("/data_queued")

	// dsFetchingPrefix is the prefix for fetching data uri fetches.
	// Structure: /data_fetching/<bid_id> -> nil.
	dsFetchingPrefix = ds.NewKey("/data_fetching")
)

// ProgressReporter reports the progress processing a deal.
type ProgressReporter interface {
	StartFetching(bidID auction.BidID, attempts uint32)
	ErrorFetching(bidID auction.BidID, attempts uint32, err error)
	StartImporting(bidID auction.BidID, attempts uint32)
	EndImporting(bidID auction.BidID, attempts uint32, err error)
	Finalized(bidID auction.BidID)
	Errored(bidID auction.BidID, errrorCause string)
}

type nullProgressReporter struct{}

func (pr nullProgressReporter) StartFetching(bidID auction.BidID, attempts uint32)            {}
func (pr nullProgressReporter) ErrorFetching(bidID auction.BidID, attempts uint32, err error) {}
func (pr nullProgressReporter) StartImporting(bidID auction.BidID, attempts uint32)           {}
func (pr nullProgressReporter) EndImporting(bidID auction.BidID, attempts uint32, err error)  {}
func (pr nullProgressReporter) Finalized(bidID auction.BidID)                                 {}
func (pr nullProgressReporter) Errored(bidID auction.BidID, errorCause string)                {}

// Bid defines the core bid model from a miner's perspective.
type Bid struct {
	ID                   auction.BidID
	AuctionID            auction.ID
	AuctioneerID         peer.ID
	PayloadCid           cid.Cid
	DealSize             uint64
	DealDuration         uint64
	ClientAddress        string
	Sources              auction.Sources
	Status               BidStatus
	AskPrice             int64 // attoFIL per GiB per epoch
	VerifiedAskPrice     int64 // attoFIL per GiB per epoch
	StartEpoch           uint64
	FastRetrieval        bool
	ProposalCid          cid.Cid
	DataURIFetchAttempts uint32
	CreatedAt            time.Time
	UpdatedAt            time.Time
	ErrorCause           string
}

// BidStatus is the status of a Bid.
type BidStatus int

const (
	// BidStatusUnspecified indicates the initial or invalid status of a bid.
	BidStatusUnspecified BidStatus = iota
	// BidStatusSubmitted indicates the bid was successfully submitted to the auctioneer.
	BidStatusSubmitted
	// BidStatusAwaitingProposal indicates the bid was accepted and is awaiting proposal cid from auctioneer.
	BidStatusAwaitingProposal
	// BidStatusQueuedData indicates the bid proposal cid was received but data fetching is queued.
	BidStatusQueuedData
	// BidStatusFetchingData indicates the bid data uri is being fetched.
	BidStatusFetchingData
	// BidStatusFinalized indicates the bid has been accepted and the data has been imported to Lotus.
	BidStatusFinalized
	// BidStatusErrored indicates a fatal error has occurred and the bid should be considered abandoned.
	BidStatusErrored
)

var bidStatusStrings = map[BidStatus]string{
	BidStatusUnspecified:      "unspecified",
	BidStatusSubmitted:        "submitted",
	BidStatusAwaitingProposal: "awaiting_proposal",
	BidStatusQueuedData:       "queued_data",
	BidStatusFetchingData:     "fetching_data",
	BidStatusFinalized:        "finalized",
	BidStatusErrored:          "errored",
}

var bidStatusByString map[string]BidStatus

func init() {
	bidStatusByString = make(map[string]BidStatus)
	for b, s := range bidStatusStrings {
		bidStatusByString[s] = b
	}
}

// String returns a string-encoded status.
func (as BidStatus) String() string {
	if s, exists := bidStatusStrings[as]; exists {
		return s
	}
	return "invalid"
}

// BidStatusByString finds a status by its string representation, or errors if
// the status does not exist.
func BidStatusByString(s string) (BidStatus, error) {
	if bs, exists := bidStatusByString[s]; exists {
		return bs, nil
	}
	return -1, errors.New("invalid bid status")
}

// Store stores miner auction deal bids.
type Store struct {
	store        txndswrap.TxnDatastore
	lc           lotusclient.LotusClient
	bytesLimiter limiter.Limiter

	jobCh  chan *Bid
	tickCh chan struct{}

	dealDataDirectory     string
	dealDataFetchAttempts uint32
	dealDataFetchTimeout  time.Duration
	dealProgressReporter  ProgressReporter

	ctx    context.Context
	cancel context.CancelFunc

	wg         sync.WaitGroup
	semImports *semaphore.Weighted // can be nil
}

// NewStore returns a new Store.
func NewStore(
	store txndswrap.TxnDatastore,
	lc lotusclient.LotusClient,
	dealDataDirectory string,
	dealDataFetchAttempts uint32,
	dealDataFetchTimeout time.Duration,
	dealProgressReporter ProgressReporter,
	discardOrphanDealsAfter time.Duration,
	bytesLimiter limiter.Limiter,
	concurrentImports int,
) (*Store, error) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Store{
		store:                 store,
		lc:                    lc,
		bytesLimiter:          bytesLimiter,
		jobCh:                 make(chan *Bid, MaxDataURIFetchConcurrency),
		tickCh:                make(chan struct{}, MaxDataURIFetchConcurrency),
		dealDataDirectory:     dealDataDirectory,
		dealDataFetchAttempts: dealDataFetchAttempts,
		dealDataFetchTimeout:  dealDataFetchTimeout,
		dealProgressReporter:  dealProgressReporter,
		ctx:                   ctx,
		cancel:                cancel,
	}
	if concurrentImports > 0 {
		s.semImports = semaphore.NewWeighted(int64(concurrentImports))
	}
	if s.dealProgressReporter == nil {
		s.dealProgressReporter = nullProgressReporter{}
	}

	if err := s.HealthCheck(); err != nil {
		return nil, fmt.Errorf("fails health check: %w", err)
	}
	// Create data fetch workers
	s.wg.Add(MaxDataURIFetchConcurrency)
	for i := 0; i < MaxDataURIFetchConcurrency; i++ {
		go s.fetchWorker(i + 1)
	}

	// Re-enqueue jobs that may have been orphaned during an forced shutdown
	if err := s.getOrphaned(); err != nil {
		return nil, fmt.Errorf("getting orphaned jobs: %w", err)
	}

	go s.startFetching()
	go s.periodicalGC(discardOrphanDealsAfter)
	return s, nil
}

// Close the store. This will wait for "fetching" data uri fetches.
func (s *Store) Close() error {
	s.cancel()
	s.wg.Wait()
	return nil
}

// SaveBid saves a bid that has been submitted to an auctioneer.
func (s *Store) SaveBid(bid Bid) error {
	if err := validate(bid); err != nil {
		return fmt.Errorf("invalid bid data: %s", err)
	}

	bid.CreatedAt = time.Now()
	if err := s.saveAndTransitionStatus(nil, &bid, BidStatusSubmitted); err != nil {
		return fmt.Errorf("saving bid: %v", err)
	}
	log.Infof("saved bid %s", bid.ID)
	return nil
}

func validate(b Bid) error {
	if b.ID == "" {
		return errors.New("id is empty")
	}
	if b.AuctionID == "" {
		return errors.New("auction id is empty")
	}
	if err := b.AuctioneerID.Validate(); err != nil {
		return fmt.Errorf("auctioneer id is not a valid peer id: %v", err)
	}
	if b.DealSize == 0 {
		return errors.New("deal size must be greater than zero")
	}
	if b.DealDuration == 0 {
		return errors.New("deal duration must be greater than zero")
	}
	if b.Status != BidStatusUnspecified {
		return errors.New("invalid initial bid status")
	}
	if b.AskPrice < 0 {
		return errors.New("ask price must be greater than or equal to zero")
	}
	if b.VerifiedAskPrice < 0 {
		return errors.New("verified ask price must be greater than or equal to zero")
	}
	if b.StartEpoch <= 0 {
		return errors.New("start epoch must be greater than zero")
	}
	if b.ProposalCid.Defined() {
		return errors.New("initial proposal cid cannot be defined")
	}
	if b.DataURIFetchAttempts != 0 {
		return errors.New("initial data uri fetch attempts must be zero")
	}
	if !b.CreatedAt.IsZero() {
		return errors.New("initial created at must be zero")
	}
	if !b.UpdatedAt.IsZero() {
		return errors.New("initial updated at must be zero")
	}
	if b.ErrorCause != "" {
		return errors.New("initial error cause must be empty")
	}
	return nil
}

// GetBid returns a bid by id.
// If a bid is not found for id, ErrBidNotFound is returned.
func (s *Store) GetBid(id auction.BidID) (*Bid, error) {
	b, err := getBid(s.store, id)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func getBid(reader ds.Read, id auction.BidID) (*Bid, error) {
	val, err := reader.Get(dsPrefix.ChildString(string(id)))
	if errors.Is(err, ds.ErrNotFound) {
		return nil, ErrBidNotFound
	} else if err != nil {
		return nil, fmt.Errorf("getting key: %v", err)
	}
	r, err := decode(val)
	if err != nil {
		return nil, fmt.Errorf("decoding value: %v", err)
	}
	return r, nil
}

// SetAwaitingProposalCid updates bid with the given sources and switch status to BidStatusAwaitingProposal. If a bid is
// not found for id, ErrBidNotFound is returned.
func (s *Store) SetAwaitingProposalCid(id auction.BidID, sources auction.Sources) error {
	if err := sources.Validate(); err != nil {
		return err
	}

	txn, err := s.store.NewTransaction(false)
	if err != nil {
		return fmt.Errorf("creating txn: %v", err)
	}
	defer txn.Discard()

	b, err := getBid(txn, id)
	if err != nil {
		return err
	}
	if b.Status == BidStatusAwaitingProposal {
		log.Infof("bid %s already in '%s', duplicated message?", b.ID, b.Status)
		return nil
	}
	if b.Status != BidStatusSubmitted {
		return fmt.Errorf("expect bid to have status '%s', got '%s'", BidStatusSubmitted, b.Status)
	}
	b.Sources = sources
	if err := s.saveAndTransitionStatus(txn, b, BidStatusAwaitingProposal); err != nil {
		return fmt.Errorf("updating bid: %v", err)
	}
	if err := txn.Commit(); err != nil {
		return fmt.Errorf("committing txn: %v", err)
	}

	log.Infof("set awaiting proposal cid for bid %s", b.ID)
	return nil
}

// SetProposalCid sets the bid proposal cid and updates status to BidStatusQueuedData.
// If a bid is not found for id, ErrBidNotFound is returned.
func (s *Store) SetProposalCid(id auction.BidID, pcid cid.Cid) error {
	if !pcid.Defined() {
		return errors.New("proposal cid must be defined")
	}

	txn, err := s.store.NewTransaction(false)
	if err != nil {
		return fmt.Errorf("creating txn: %v", err)
	}
	defer txn.Discard()

	b, err := getBid(txn, id)
	if err != nil {
		return err
	}
	if b.Status > BidStatusAwaitingProposal {
		log.Infof("bid %s already in '%s', duplicated message?", b.ID, b.Status)
		return nil
	}
	if b.Status != BidStatusAwaitingProposal {
		return fmt.Errorf("expect bid to have status '%s', got '%s'", BidStatusAwaitingProposal, b.Status)
	}

	b.ProposalCid = pcid
	if err := s.enqueueDataURI(txn, b); err != nil {
		return fmt.Errorf("enqueueing data uri: %v", err)
	}
	if err := txn.Commit(); err != nil {
		return fmt.Errorf("committing txn: %v", err)
	}

	log.Infof("set proposal cid for bid %s; enqueued sources %s for fetch", b.ID, b.Sources)
	return nil
}

// Query is used to query for bids.
type Query struct {
	Offset string
	Order  Order
	Limit  int
}

func (q Query) setDefaults() Query {
	if q.Limit == -1 {
		q.Limit = maxListLimit
	} else if q.Limit <= 0 {
		q.Limit = defaultListLimit
	} else if q.Limit > maxListLimit {
		q.Limit = maxListLimit
	}
	return q
}

// Order specifies the order of list results.
// Default is decending by time created.
type Order int

const (
	// OrderDescending orders results decending.
	OrderDescending Order = iota
	// OrderAscending orders results ascending.
	OrderAscending
)

// ListBids lists bids by applying a Query.
func (s *Store) ListBids(query Query) ([]*Bid, error) {
	query = query.setDefaults()

	var (
		order dsq.Order
		seek  string
		limit = query.Limit
	)

	if len(query.Offset) != 0 {
		seek = dsPrefix.ChildString(query.Offset).String()
		limit++
	}

	switch query.Order {
	case OrderDescending:
		order = dsq.OrderByKeyDescending{}
		if len(seek) == 0 {
			// Seek to largest possible key and decend from there
			seek = dsPrefix.ChildString(
				strings.ToLower(ulid.MustNew(ulid.MaxTime(), nil).String())).String()
		}
	case OrderAscending:
		order = dsq.OrderByKey{}
	}

	results, err := s.store.QueryExtended(dsextensions.QueryExt{
		Query: dsq.Query{
			Prefix: dsPrefix.String(),
			Orders: []dsq.Order{order},
			Limit:  limit,
		},
		SeekPrefix: seek,
	})
	if err != nil {
		return nil, fmt.Errorf("querying requests: %v", err)
	}
	defer func() {
		if err := results.Close(); err != nil {
			log.Errorf("closing results: %v", err)
		}
	}()

	var list []*Bid
	for res := range results.Next() {
		if res.Error != nil {
			return nil, fmt.Errorf("getting next result: %v", res.Error)
		}
		b, err := decode(res.Value)
		if err != nil {
			return nil, fmt.Errorf("decoding value: %v", err)
		}
		list = append(list, b)
	}

	// Remove seek from list
	if len(query.Offset) != 0 && len(list) > 0 {
		list = list[1:]
	}

	return list, nil
}

// WriteDealData writes the deal data to the configured deal data directory.
func (s *Store) WriteDealData(b *Bid) (string, error) {
	if b.Sources.CARURL != nil {
		return s.WriteDataURI(b.ID, b.PayloadCid.String(), b.Sources.CARURL.URL.String(), b.DealSize)
	}
	return "", errors.New("not implemented")
}

// WriteDataURI writes the uri resource to the configured deal data directory.
func (s *Store) WriteDataURI(bidID auction.BidID, payloadCid, uri string, size uint64) (string, error) {
	duri, err := datauri.NewURI(payloadCid, uri)
	if err != nil {
		return "", fmt.Errorf("parsing data uri: %w", err)
	}
	f, err := os.Create(s.dealDataFilePathFor(bidID, payloadCid))
	if err != nil {
		return "", fmt.Errorf("opening file for deal data: %v", err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			log.Errorf("closing data file: %v", err)
		}
	}()

	if _, err := f.Seek(0, 0); err != nil {
		return "", fmt.Errorf("seeking file to the beginning: %v", err)
	}
	log.Debugf("fetching %s with timeout of %v", uri, s.dealDataFetchTimeout)
	ctx, cancel := context.WithTimeout(s.ctx, s.dealDataFetchTimeout)
	defer cancel()
	if err := duri.Write(ctx, f); err != nil {
		return "", fmt.Errorf("writing data uri %s: %w", uri, err)
	}
	return f.Name(), nil
}

// HealthCheck checks if the store is healthy enough to participate in bidding.
func (s *Store) HealthCheck() error {
	// make sure the directory is writable
	f, err := ioutil.TempFile(s.dealDataDirectory, ".touch")
	if err != nil {
		return fmt.Errorf("deal data directory: %v", err)
	}
	if err = os.Remove(f.Name()); err != nil {
		log.Errorf("removing temp file: %v", err)
	}
	if err := s.lc.HealthCheck(); err != nil {
		return fmt.Errorf("lotus client: %v", err)
	}
	return nil
}

func (s *Store) dealDataFilePathFor(bidID auction.BidID, payloadCid string) string {
	return filepath.Join(s.dealDataDirectory, fmt.Sprintf("%s_%s", payloadCid, bidID))
}

// enqueueDataURI queues a data uri fetch.
func (s *Store) enqueueDataURI(txn ds.Txn, b *Bid) error {
	// Set the bid to "fetching_data"
	if err := s.saveAndTransitionStatus(txn, b, BidStatusFetchingData); err != nil {
		return fmt.Errorf("updating status (fetching_data): %v", err)
	}
	select {
	case s.jobCh <- b:
	default:
		log.Debugf("workers are busy; queueing %s", b.ID)
		// Workers are busy, set back to "queued_data"
		if err := s.saveAndTransitionStatus(txn, b, BidStatusQueuedData); err != nil {
			log.Errorf("updating status (queued_data): %v", err)
		}
	}
	return nil
}

func (s *Store) fetchWorker(num int) {
	defer s.wg.Done()

	fail := func(b *Bid, err error) (status BidStatus) {
		b.ErrorCause = err.Error()
		if b.DataURIFetchAttempts >= s.dealDataFetchAttempts {
			status = BidStatusErrored
			s.bytesLimiter.Withdraw(string(b.AuctionID))
			s.dealProgressReporter.Errored(b.ID, b.ErrorCause)
			log.Warnf("job %s exhausted all %d attempts with error: %v", b.ID, s.dealDataFetchAttempts, err)
		} else {
			status = BidStatusQueuedData
			log.Debugf("retrying job %s with error: %v", b.ID, err)
		}
		return status
	}

	for {
		select {
		case <-s.ctx.Done():
			return

		case b := <-s.jobCh:
			if s.ctx.Err() != nil {
				return
			}
			log.Infof("fetching sources %s", b.Sources)
			b.DataURIFetchAttempts++
			log.Debugf(
				"worker %d got job %s (attempt=%d/%d)", num, b.ID, b.DataURIFetchAttempts, s.dealDataFetchAttempts)

			// Fetch the data cid
			var (
				status BidStatus
				logMsg string
			)
			s.dealProgressReporter.StartFetching(b.ID, b.DataURIFetchAttempts)
			file, err := s.WriteDealData(b)
			if err != nil {
				s.dealProgressReporter.ErrorFetching(b.ID, b.DataURIFetchAttempts, err)
				status = fail(b, err)
				logMsg = fmt.Sprintf("status=%s error=%s", status, b.ErrorCause)
			} else {
				log.Infof("importing %s to lotus with proposal cid %s", file, b.ProposalCid)

				// if requested, limit the number of concurrent imports
				if s.semImports != nil {
					// the error returned by Acquire is
					// always ctx.Err(), in this case never
					// happens.
					_ = s.semImports.Acquire(context.Background(), 1)
				}
				s.dealProgressReporter.StartImporting(b.ID, b.DataURIFetchAttempts)
				err = s.lc.ImportData(b.ProposalCid, file)
				if s.semImports != nil {
					s.semImports.Release(1)
				}

				s.dealProgressReporter.EndImporting(b.ID, b.DataURIFetchAttempts, err)
				if err != nil {
					status = fail(b, err)
					logMsg = fmt.Sprintf("status=%s error=%s", status, b.ErrorCause)
				} else {
					status = BidStatusFinalized
					s.bytesLimiter.Commit(string(b.AuctionID))
					s.dealProgressReporter.Finalized(b.ID)
					// Reset error
					b.ErrorCause = ""
					logMsg = fmt.Sprintf("status=%s", status)
				}
			}
			log.Infof("finished fetching data cid %s (%s)", b.ID, logMsg)

			// Save and update status to "finalized"
			if err := s.saveAndTransitionStatus(nil, b, status); err != nil {
				log.Errorf("updating status (%s): %v", status, err)
			}

			log.Debugf("worker %d finished job %s", num, b.ID)
			select {
			case s.tickCh <- struct{}{}:
			default:
			}
		}
	}
}

func (s *Store) startFetching() {
	t := time.NewTimer(DataURIFetchStartDelay)
	for {
		select {
		case <-s.ctx.Done():
			t.Stop()
			return
		case <-t.C:
			s.getNext()
		case <-s.tickCh:
			s.getNext()
		}
	}
}

func (s *Store) periodicalGC(discardOrphanDealsAfter time.Duration) {
	tk := time.NewTicker(gcInterval)
	for range tk.C {
		if s.ctx.Err() != nil {
			return
		}
		start := time.Now()
		bidsRemoved, filesRemoved := s.GC(discardOrphanDealsAfter)
		log.Infof("GC finished in %v: %d orphan deals cleaned, %d deal data files cleaned",
			time.Since(start), bidsRemoved, filesRemoved)
	}
}

// GC cleans up deal data files, if discardOrphanDealsAfter is not zero, it
// also removes bids staying at BidStatusAwaitingProposal for that longer.
func (s *Store) GC(discardOrphanDealsAfter time.Duration) (bidsRemoved, filesRemoved int) {
	bids, err := s.ListBids(Query{})
	if err != nil {
		log.Errorf("listing bids: %v", err)
		return
	}
	keepFiles := make(map[string]struct{})
	removeFiles := make(map[string]struct{})
	for _, bid := range bids {
		if discardOrphanDealsAfter > 0 &&
			bid.Status == BidStatusAwaitingProposal &&
			time.Since(bid.UpdatedAt) > discardOrphanDealsAfter {
			log.Debugf("discard deal %s for it has no progress for %v: %+v",
				bid.ID, time.Since(bid.UpdatedAt), *bid)
			err = s.store.Delete(dsPrefix.ChildString(string(bid.ID)))
			if err != nil {
				log.Errorf("failed to remove deal %s: %v", bid.ID, err)
			} else {
				bidsRemoved++
			}
		} else if bid.Status == BidStatusFinalized {
			log.Debugf("will remove deal data for finalized deal %s", bid.ID)
			removeFiles[s.dealDataFilePathFor(bid.ID, bid.PayloadCid.String())] = struct{}{}
		} else {
			keepFiles[s.dealDataFilePathFor(bid.ID, bid.PayloadCid.String())] = struct{}{}
		}
	}
	walk := func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Errorf("error walking into %s: %v", path, err)
			return nil
		}
		if info.IsDir() {
			return nil
		}
		shouldRemove := false
		if _, exists := removeFiles[path]; exists {
			shouldRemove = true
		} else if _, exists := keepFiles[path]; !exists {
			if discardOrphanDealsAfter > 0 && time.Since(info.ModTime()) > discardOrphanDealsAfter {
				shouldRemove = true
			}
		}
		if shouldRemove {
			if err := os.Remove(path); err != nil {
				log.Errorf("error removing %s: %v", path, err)
				return nil
			}
			log.Debugf("removed %s", path)
			filesRemoved++
		}
		return nil
	}
	err = filepath.Walk(s.dealDataDirectory, walk)
	if err != nil {
		log.Errorf("error walking deal data directory: %v", err)
	}
	return
}

func (s *Store) getNext() {
	txn, err := s.store.NewTransaction(false)
	if err != nil {
		log.Errorf("creating txn: %v", err)
		return
	}
	defer txn.Discard()

	b, err := s.getQueued(txn)
	if err != nil {
		log.Errorf("getting next in queued: %v", err)
		return
	}
	if b == nil {
		return
	}
	log.Debugf("enqueueing job: %s", b.ID)
	if err := s.enqueueDataURI(txn, b); err != nil {
		log.Errorf("enqueueing: %v", err)
	}
	if err := txn.Commit(); err != nil {
		log.Errorf("committing txn: %v", err)
	}
}

func (s *Store) getQueued(txn ds.Txn) (*Bid, error) {
	results, err := txn.Query(dsq.Query{
		Prefix:   dsQueuedPrefix.String(),
		Orders:   []dsq.Order{dsq.OrderByKey{}},
		Limit:    1,
		KeysOnly: true,
	})
	if err != nil {
		return nil, fmt.Errorf("querying queued: %v", err)
	}
	defer func() {
		if err := results.Close(); err != nil {
			log.Errorf("closing results: %v", err)
		}
	}()

	res, ok := <-results.Next()
	if !ok {
		return nil, nil
	} else if res.Error != nil {
		return nil, fmt.Errorf("getting next result: %v", res.Error)
	}

	b, err := getBid(txn, auction.BidID(path.Base(res.Key)))
	if err != nil {
		return nil, fmt.Errorf("getting bid: %v", err)
	}
	return b, nil
}

func (s *Store) getOrphaned() error {
	txn, err := s.store.NewTransaction(false)
	if err != nil {
		return fmt.Errorf("creating txn: %v", err)
	}
	defer txn.Discard()

	bids, err := s.getFetching(txn)
	if err != nil {
		return fmt.Errorf("getting next in queued: %v", err)
	}
	if len(bids) == 0 {
		return nil
	}

	for _, b := range bids {
		log.Debugf("enqueueing orphaned job: %s", b.ID)
		if err := s.enqueueDataURI(txn, &b); err != nil {
			return fmt.Errorf("enqueueing: %v", err)
		}
	}
	if err := txn.Commit(); err != nil {
		return fmt.Errorf("committing txn: %v", err)
	}
	return nil
}

func (s *Store) getFetching(txn ds.Txn) ([]Bid, error) {
	results, err := txn.Query(dsq.Query{
		Prefix:   dsFetchingPrefix.String(),
		Orders:   []dsq.Order{dsq.OrderByKey{}},
		KeysOnly: true,
	})
	if err != nil {
		return nil, fmt.Errorf("querying queued: %v", err)
	}
	defer func() {
		if err := results.Close(); err != nil {
			log.Errorf("closing results: %v", err)
		}
	}()

	var bids []Bid
	for res := range results.Next() {
		if res.Error != nil {
			return nil, fmt.Errorf("getting next result: %v", res.Error)
		}
		b, err := getBid(txn, auction.BidID(path.Base(res.Key)))
		if err != nil {
			return nil, fmt.Errorf("getting bid: %v", err)
		}
		bids = append(bids, *b)
	}
	return bids, nil
}

// saveAndTransitionStatus saves bid state and transitions to a new status.
// Do not directly edit the bids status because it is needed to determine the correct status transition.
// Pass the desired new status with newStatus.
func (s *Store) saveAndTransitionStatus(txn ds.Txn, b *Bid, newStatus BidStatus) error {
	commitTxn := txn == nil
	if commitTxn {
		var err error
		txn, err = s.store.NewTransaction(false)
		if err != nil {
			return fmt.Errorf("creating txn: %v", err)
		}
		defer txn.Discard()
	}

	if b.Status != newStatus {
		// Handle currently "queued_data" and "fetching_data" status
		if b.Status == BidStatusQueuedData {
			if err := txn.Delete(dsQueuedPrefix.ChildString(string(b.ID))); err != nil {
				return fmt.Errorf("deleting from queued: %v", err)
			}
		} else if b.Status == BidStatusFetchingData {
			if err := txn.Delete(dsFetchingPrefix.ChildString(string(b.ID))); err != nil {
				return fmt.Errorf("deleting from fetching: %v", err)
			}
		}
		if newStatus == BidStatusQueuedData {
			if err := txn.Put(dsQueuedPrefix.ChildString(string(b.ID)), nil); err != nil {
				return fmt.Errorf("putting to queued: %v", err)
			}
		} else if newStatus == BidStatusFetchingData {
			if err := txn.Put(dsFetchingPrefix.ChildString(string(b.ID)), nil); err != nil {
				return fmt.Errorf("putting to fetching: %v", err)
			}
		}
		// Update status
		b.Status = newStatus
	}

	b.UpdatedAt = time.Now()
	if b.UpdatedAt.IsZero() {
		b.UpdatedAt = b.CreatedAt
	}

	val, err := encode(b)
	if err != nil {
		return fmt.Errorf("encoding value: %v", err)
	}
	if err := txn.Put(dsPrefix.ChildString(string(b.ID)), val); err != nil {
		return fmt.Errorf("putting value: %v", err)
	}
	if commitTxn {
		if err := txn.Commit(); err != nil {
			return fmt.Errorf("committing txn: %v", err)
		}
	}
	return nil
}

func encode(v interface{}) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decode(v []byte) (b *Bid, err error) {
	dec := gob.NewDecoder(bytes.NewReader(v))
	if err := dec.Decode(&b); err != nil {
		return b, err
	}
	return b, nil
}
