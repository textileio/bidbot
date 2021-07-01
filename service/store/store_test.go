package store

import (
	"crypto/rand"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ipfs/go-cid"
	blockstore "github.com/ipfs/go-ipfs-blockstore"
	util "github.com/ipfs/go-ipfs-util"
	format "github.com/ipfs/go-ipld-format"
	"github.com/ipld/go-car"
	"github.com/libp2p/go-libp2p-core/crypto"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/textileio/bidbot/lib/broker"
	"github.com/textileio/bidbot/lib/logging"
	"github.com/textileio/bidbot/lib/marketpeer"
	lotusclientmocks "github.com/textileio/bidbot/lib/mocks/lotusclient"
	"github.com/textileio/bidbot/service/datauri/apitest"
	"github.com/textileio/bidbot/service/limiter"
	badger "github.com/textileio/go-ds-badger3"
	golog "github.com/textileio/go-log/v2"
)

func init() {
	if err := logging.SetLogLevels(map[string]golog.LogLevel{
		"bidbot/store": golog.LevelDebug,
	}); err != nil {
		panic(err)
	}

	DataURIFetchTimeout = time.Second * 5
}

func TestStore_ListBids(t *testing.T) {
	t.Parallel()
	s, dag, _ := newStore(t)
	gw := apitest.NewDataURIHTTPGateway(dag)
	t.Cleanup(gw.Close)

	sk, _, err := crypto.GenerateEd25519Key(rand.Reader)
	require.NoError(t, err)
	auctioneerID, err := peer.IDFromPrivateKey(sk)
	require.NoError(t, err)

	limit := 100
	now := time.Now()
	ids := make([]broker.BidID, limit)
	for i := 0; i < limit; i++ {
		now = now.Add(time.Millisecond)
		id := broker.BidID(strings.ToLower(ulid.MustNew(ulid.Timestamp(now), rand.Reader).String()))
		aid := broker.AuctionID(strings.ToLower(ulid.MustNew(ulid.Now(), rand.Reader).String()))
		_, sources, err := gw.CreateHTTPSources(true)
		require.NoError(t, err)

		err = s.SaveBid(Bid{
			ID:               id,
			AuctionID:        aid,
			AuctioneerID:     auctioneerID,
			DealSize:         1024,
			DealDuration:     1000,
			Sources:          sources,
			AskPrice:         100,
			VerifiedAskPrice: 100,
			StartEpoch:       2000,
		})
		require.NoError(t, err)
		ids[i] = id
	}

	// Empty query, should return newest 10 records
	l, err := s.ListBids(Query{})
	require.NoError(t, err)
	assert.Len(t, l, 10)
	assert.Equal(t, ids[limit-1], l[0].ID)
	assert.Equal(t, ids[limit-10], l[9].ID)

	// Get next page, should return next 10 records
	offset := l[len(l)-1].ID
	l, err = s.ListBids(Query{Offset: string(offset)})
	require.NoError(t, err)
	assert.Len(t, l, 10)
	assert.Equal(t, ids[limit-11], l[0].ID)
	assert.Equal(t, ids[limit-20], l[9].ID)

	// Get previous page, should return the first page in reverse order
	offset = l[0].ID
	l, err = s.ListBids(Query{Offset: string(offset), Order: OrderAscending})
	require.NoError(t, err)
	assert.Len(t, l, 10)
	assert.Equal(t, ids[limit-10], l[0].ID)
	assert.Equal(t, ids[limit-1], l[9].ID)
}

func TestStore_SaveBid(t *testing.T) {
	t.Parallel()
	s, dag, _ := newStore(t)
	bid := newBid(t, dag, true)
	err := s.SaveBid(*bid)
	require.NoError(t, err)

	got, err := s.GetBid(bid.ID)
	require.NoError(t, err)
	assert.Equal(t, bid.ID, got.ID)
	assert.Equal(t, bid.AuctionID, got.AuctionID)
	assert.Equal(t, bid.Sources, got.Sources)
	assert.Equal(t, 1024, int(got.DealSize))
	assert.Equal(t, 1000, int(got.DealDuration))
	assert.Equal(t, BidStatusSubmitted, got.Status)
	assert.Equal(t, 100, int(got.AskPrice))
	assert.Equal(t, 100, int(got.VerifiedAskPrice))
	assert.Equal(t, 2000, int(got.StartEpoch))
	assert.False(t, got.FastRetrieval)
	assert.False(t, got.ProposalCid.Defined())
	assert.Equal(t, 0, int(got.DataURIFetchAttempts))
	assert.False(t, got.CreatedAt.IsZero())
	assert.False(t, got.UpdatedAt.IsZero())
	assert.Empty(t, got.ErrorCause)
}

func TestStore_StatusProgression(t *testing.T) {
	t.Parallel()
	s, dag, bs := newStore(t)
	t.Run("happy path", func(t *testing.T) {
		bid := newBid(t, dag, true)
		err := s.SaveBid(*bid)
		require.NoError(t, err)

		id := bid.ID
		got, err := s.GetBid(id)
		require.NoError(t, err)
		assert.Equal(t, BidStatusSubmitted, got.Status)

		err = s.SetAwaitingProposalCid(id)
		require.NoError(t, err)
		got, err = s.GetBid(id)
		require.NoError(t, err)
		assert.Equal(t, BidStatusAwaitingProposal, got.Status)

		err = s.SetProposalCid(id, cid.NewCidV1(cid.Raw, util.Hash([]byte("howdy"))))
		require.NoError(t, err)
		got, err = s.GetBid(id)
		require.NoError(t, err)
		assert.Equal(t, BidStatusFetchingData, got.Status)

		// Allow to finish
		time.Sleep(time.Second * 1)

		got, err = s.GetBid(id)
		require.NoError(t, err)
		assert.Equal(t, BidStatusFinalized, got.Status)
		assert.Empty(t, got.ErrorCause)

		// Check if car file was written to proposal data directory
		f, err := os.Open(s.dealDataFilePathFor(id, bid.PayloadCid.String()))
		require.NoError(t, err)
		defer func() { _ = f.Close() }()
		h, err := car.LoadCar(bs, f)
		require.NoError(t, err)
		require.Len(t, h.Roots, 1)
		require.True(t, h.Roots[0].Equals(bid.PayloadCid))
	})

	t.Run("unreachable data uri", func(t *testing.T) {
		bid := newBid(t, dag, false)
		err := s.SaveBid(*bid)
		require.NoError(t, err)

		id := bid.ID
		got, err := s.GetBid(id)
		require.NoError(t, err)
		assert.Equal(t, BidStatusSubmitted, got.Status)

		err = s.SetAwaitingProposalCid(id)
		require.NoError(t, err)
		got, err = s.GetBid(id)
		require.NoError(t, err)
		assert.Equal(t, BidStatusAwaitingProposal, got.Status)

		err = s.SetProposalCid(id, cid.NewCidV1(cid.Raw, util.Hash([]byte("howdy"))))
		require.NoError(t, err)

		// Allow to finish
		time.Sleep(time.Second * 1)

		got, err = s.GetBid(id)
		require.NoError(t, err)
		assert.Equal(t, BidStatusErrored, got.Status)
		assert.NotEmpty(t, got.ErrorCause)
		assert.Equal(t, 2, int(got.DataURIFetchAttempts))
	})
}

func TestStore_GC(t *testing.T) {
	s, dag, _ := newStore(t)
	successfulBid := newBid(t, dag, true)
	failBid := newBid(t, dag, false)
	hangingBid := newBid(t, dag, false)
	_ = s.SaveBid(*successfulBid)
	_ = s.SaveBid(*failBid)
	_ = s.SaveBid(*hangingBid)

	_ = s.SetAwaitingProposalCid(successfulBid.ID)
	_ = s.SetProposalCid(successfulBid.ID, cid.NewCidV1(cid.Raw, util.Hash([]byte("howdy"))))
	_ = s.SetAwaitingProposalCid(failBid.ID)
	_ = s.SetProposalCid(failBid.ID, cid.NewCidV1(cid.Raw, util.Hash([]byte("howdy"))))
	_ = s.SetAwaitingProposalCid(hangingBid.ID)

	// Allow to finish
	time.Sleep(time.Second * 1)

	successfulBid, _ = s.GetBid(successfulBid.ID)
	assert.Equal(t, BidStatusFinalized, successfulBid.Status)
	successFile := s.dealDataFilePathFor(successfulBid.ID, successfulBid.PayloadCid.String())
	_, err := os.Stat(successFile)
	require.NoError(t, err)

	failBid, _ = s.GetBid(failBid.ID)
	assert.Equal(t, BidStatusErrored, failBid.Status)
	failFile := s.dealDataFilePathFor(failBid.ID, failBid.PayloadCid.String())
	// simulate leftover file for failed deals
	f, err := os.Create(failFile)
	require.NoError(t, err)
	_ = f.Close()

	hangingBid, _ = s.GetBid(hangingBid.ID)
	assert.Equal(t, BidStatusAwaitingProposal, hangingBid.Status)

	// GC without discarding orphan deals
	s.GC(0)
	_, err = os.Stat(successFile)
	require.True(t, os.IsNotExist(err), "file for finalized deal should have been removed")
	_, err = os.Stat(failFile)
	require.NoError(t, err, "file for errored deal should always be kept")
	_, err = s.GetBid(hangingBid.ID)
	require.NoError(t, err, "not cleaning orphan deals")

	// GC with discarding orphan deals
	s.GC(time.Second)
	_, err = os.Stat(failFile)
	require.NoError(t, err, "file for errored deal should always be kept")
	//
	_, err = s.GetBid(hangingBid.ID)
	require.Equal(t, ErrBidNotFound, err, "orphan deals should have been cleaned up")
}

func newStore(t *testing.T) (*Store, format.DAGService, blockstore.Blockstore) {
	ds, err := badger.NewDatastore(t.TempDir(), &badger.DefaultOptions)
	require.NoError(t, err)
	sk, _, err := crypto.GenerateEd25519Key(rand.Reader)
	require.NoError(t, err)
	p, err := marketpeer.New(marketpeer.Config{
		RepoPath: t.TempDir(),
		PrivKey:  sk,
	})
	require.NoError(t, err)
	s, err := NewStore(ds, p.Host(), p.DAGService(), newLotusClientMock(), t.TempDir(), 2, 0, limiter.NopeLimiter{})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, s.Close())
		require.NoError(t, ds.Close())
	})
	return s, p.DAGService(), p.BlockStore()
}

func newLotusClientMock() *lotusclientmocks.LotusClient {
	lc := &lotusclientmocks.LotusClient{}
	lc.On("HealthCheck").Return(nil)
	lc.On(
		"ImportData",
		mock.Anything,
		mock.Anything,
	).Return(nil)
	lc.On("Close").Return(nil)
	return lc
}

func newBid(t *testing.T, dag format.DAGService, carAccessible bool) *Bid {
	gw := apitest.NewDataURIHTTPGateway(dag)
	t.Cleanup(gw.Close)
	payloadCid, sources, err := gw.CreateHTTPSources(carAccessible)
	require.NoError(t, err)

	sk, _, err := crypto.GenerateEd25519Key(rand.Reader)
	require.NoError(t, err)
	auctioneerID, err := peer.IDFromPrivateKey(sk)
	require.NoError(t, err)

	now := time.Now()
	id := broker.BidID(strings.ToLower(ulid.MustNew(ulid.Timestamp(now), rand.Reader).String()))
	aid := broker.AuctionID(strings.ToLower(ulid.MustNew(ulid.Now(), rand.Reader).String()))
	return &Bid{
		ID:               id,
		AuctionID:        aid,
		AuctioneerID:     auctioneerID,
		PayloadCid:       payloadCid,
		DealSize:         1024,
		DealDuration:     1000,
		Sources:          sources,
		AskPrice:         100,
		VerifiedAskPrice: 100,
		StartEpoch:       2000,
	}
}
