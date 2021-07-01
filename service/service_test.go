package service_test

import (
	"crypto/rand"
	"path/filepath"
	"testing"

	"github.com/libp2p/go-libp2p-core/crypto"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	core "github.com/textileio/bidbot/lib/broker"
	"github.com/textileio/bidbot/lib/dshelper"
	"github.com/textileio/bidbot/lib/logging"
	"github.com/textileio/bidbot/lib/marketpeer"
	filclientmocks "github.com/textileio/bidbot/lib/mocks/filclient"
	lotusclientmocks "github.com/textileio/bidbot/lib/mocks/lotusclient"
	"github.com/textileio/bidbot/service"
	golog "github.com/textileio/go-log/v2"
)

const (
	oneDayEpochs = 60 * 24 * 2
)

func init() {
	if err := logging.SetLogLevels(map[string]golog.LogLevel{
		"bidbot/service": golog.LevelDebug,
		"bidbot/store":   golog.LevelDebug,
		"mpeer":          golog.LevelDebug,
	}); err != nil {
		panic(err)
	}
}

func TestNew(t *testing.T) {
	dir := t.TempDir()

	priv, _, err := crypto.GenerateEd25519Key(rand.Reader)
	require.NoError(t, err)

	bidParams := service.BidParams{
		WalletAddrSig:         []byte("bar"),
		AskPrice:              100000000000,
		VerifiedAskPrice:      100000000000,
		FastRetrieval:         true,
		DealStartWindow:       oneDayEpochs,
		DealDataFetchAttempts: 3,
		DealDataDirectory:     t.TempDir(),
	}
	auctionFilters := service.AuctionFilters{
		DealDuration: service.MinMaxFilter{
			Min: core.MinDealDuration,
			Max: core.MaxDealDuration,
		},
		DealSize: service.MinMaxFilter{
			Min: 56 * 1024,
			Max: 32 * 1000 * 1000 * 1000,
		},
	}

	config := service.Config{
		Peer: marketpeer.Config{
			PrivKey:    priv,
			RepoPath:   dir,
			EnableMDNS: true,
		},
	}

	store, err := dshelper.NewBadgerTxnDatastore(filepath.Join(dir, "bidstore"))
	require.NoError(t, err)
	defer func() { _ = store.Close() }()

	lc := newLotusClientMock()
	fc := newFilClientMock()

	config.AuctionFilters = auctionFilters

	// Bad DealStartWindow
	config.BidParams = service.BidParams{
		DealStartWindow:       0,
		DealDataFetchAttempts: 1,
		DealDataDirectory:     t.TempDir(),
	}
	_, err = service.New(config, store, lc, fc)
	require.Error(t, err)

	// Bad DealDataFetchAttempts
	config.BidParams = service.BidParams{
		DealStartWindow:       oneDayEpochs,
		DealDataFetchAttempts: 0,
		DealDataDirectory:     t.TempDir(),
	}
	_, err = service.New(config, store, lc, fc)
	require.Error(t, err)

	// Bad DealDataDirectory
	config.BidParams = service.BidParams{
		DealStartWindow:       oneDayEpochs,
		DealDataFetchAttempts: 1,
		DealDataDirectory:     "",
	}
	_, err = service.New(config, store, lc, fc)
	require.Error(t, err)

	config.BidParams = bidParams

	// Bad auction MinMaxFilter
	config.AuctionFilters = service.AuctionFilters{
		DealDuration: service.MinMaxFilter{
			Min: 10, // min greater than max
			Max: 0,
		},
		DealSize: service.MinMaxFilter{
			Min: 10,
			Max: 20,
		},
	}
	_, err = service.New(config, store, lc, fc)
	require.Error(t, err)

	config.AuctionFilters = auctionFilters

	// Good config
	s, err := service.New(config, store, lc, fc)
	require.NoError(t, err)
	err = s.Subscribe(false)
	require.NoError(t, err)
	require.NoError(t, s.Close())

	// Ensure verify bidder can lead to error
	fc2 := &filclientmocks.FilClient{}
	fc2.On(
		"VerifyBidder",
		mock.Anything,
		mock.Anything,
		mock.Anything,
	).Return(false, nil)
	_, err = service.New(config, store, lc, fc2)
	require.Error(t, err)
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

func newFilClientMock() *filclientmocks.FilClient {
	fc := &filclientmocks.FilClient{}
	fc.On(
		"VerifyBidder",
		mock.Anything,
		mock.Anything,
		mock.Anything,
	).Return(true, nil)
	fc.On("GetChainHeight").Return(uint64(0), nil)
	fc.On("Close").Return(nil)
	return fc
}
