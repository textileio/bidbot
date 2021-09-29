package service_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ipfs/go-cid"
	util "github.com/ipfs/go-ipfs-util"
	"github.com/libp2p/go-libp2p-core/crypto"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	pb "github.com/textileio/bidbot/gen/v1"
	core "github.com/textileio/bidbot/lib/auction"
	"github.com/textileio/bidbot/lib/cast"
	"github.com/textileio/bidbot/lib/datauri/apitest"
	"github.com/textileio/bidbot/lib/dshelper"
	"github.com/textileio/bidbot/lib/dshelper/txndswrap"
	filclientmocks "github.com/textileio/bidbot/mocks/lib/filclient"
	lotusclientmocks "github.com/textileio/bidbot/mocks/service/lotusclient"
	"github.com/textileio/bidbot/service"
	"github.com/textileio/bidbot/service/limiter"
	tcrypto "github.com/textileio/crypto"
	rpc "github.com/textileio/go-libp2p-pubsub-rpc"
	rpcpeer "github.com/textileio/go-libp2p-pubsub-rpc/peer"
	golog "github.com/textileio/go-log/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	oneDayEpochs = 60 * 24 * 2
)

func init() {
	if err := golog.SetLogLevels(map[string]golog.LogLevel{
		"bidbot/service": golog.LevelDebug,
		"bidbot/store":   golog.LevelDebug,
		"psrpc/peer":     golog.LevelDebug,
	}); err != nil {
		panic(err)
	}
}

func TestNew(t *testing.T) {
	// Bad DealStartWindow
	_, err := newService(t, func(config *service.Config) {
		config.BidParams = service.BidParams{
			DealStartWindow:       0,
			DealDataFetchAttempts: 1,
			DealDataDirectory:     t.TempDir(),
		}
	})
	require.Error(t, err)

	// Bad DealDataFetchAttempts
	_, err = newService(t, func(config *service.Config) {
		config.BidParams = service.BidParams{
			DealStartWindow:       oneDayEpochs,
			DealDataFetchAttempts: 0,
			DealDataDirectory:     t.TempDir(),
		}
	})
	require.Error(t, err)

	// Bad DealDataDirectory
	_, err = newService(t, func(config *service.Config) {
		config.BidParams = service.BidParams{
			DealStartWindow:       oneDayEpochs,
			DealDataFetchAttempts: 1,
			DealDataDirectory:     "",
		}
	})
	require.Error(t, err)

	// Bad auction MinMaxFilter
	_, err = newService(t, func(config *service.Config) {
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
	})
	require.Error(t, err)

	// Good config
	s, err := newService(t, nil)
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
	config, store := validConfig(t)
	lc := newLotusClientMock()
	_, err = service.New(config, store, lc, fc2)
	require.Error(t, err)
}

func TestBytesLimit(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	service.BidsExpiration = 2 * time.Second
	peerConfig, _ := newPeerConfig(t)
	mockAuctioneer, err := rpcpeer.New(peerConfig)
	require.NoError(t, err)
	gw := apitest.NewDataURIHTTPGateway(mockAuctioneer.DAGService())
	t.Cleanup(gw.Close)
	payloadCid, sources, err := gw.CreateHTTPSources(true)
	require.NoError(t, err)

	limitPeriod := 5 * time.Second
	limitBytes := uint64(80000)
	var encryptKey tcrypto.EncryptionKey
	s, err := newService(t, func(config *service.Config) {
		config.BytesLimiter = limiter.NewRunningTotalLimiter(limitBytes, limitPeriod)
		pubKey, err := crypto.MarshalPublicKey(config.Peer.PrivKey.GetPublic())
		require.NoError(t, err)
		encryptKey, err = tcrypto.EncryptionKeyFromBytes(pubKey)
		require.NoError(t, err)
	})
	require.NoError(t, err)
	require.NoError(t, s.Subscribe(true))

	auctions, err := mockAuctioneer.NewTopic(context.Background(), core.Topic, false)
	require.NoError(t, err)
	var createTopicOnce sync.Once
	var wins *rpc.Topic
	var proposals *rpc.Topic
	chWinsResponse := make(chan error)
	runAuction := func(t *testing.T, auctionID string, expectGoodResponse bool, sendProposal bool) {
		auction := &pb.Auction{
			Id:               auctionID,
			PayloadCid:       payloadCid.String(),
			DealSize:         limitBytes * 4 / 5,
			DealDuration:     core.MinDealDuration,
			FilEpochDeadline: 3000,
			EndsAt:           timestamppb.New(time.Now().Add(time.Minute)),
		}

		msg, err := proto.Marshal(auction)
		require.NoError(t, err)
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(time.Second))
		defer cancel()
		topic, err := mockAuctioneer.NewTopic(ctx, core.BidsTopic(core.ID(auctionID)), true)
		require.NoError(t, err)
		topic.SetMessageHandler(func(from peer.ID, _ string, msg []byte) ([]byte, error) {
			pbid := &pb.Bid{}
			require.NoError(t, proto.Unmarshal(msg, pbid))
			bidID := "bid-" + pbid.AuctionId
			go func() {
				// avoid conflict between saving bid and writing wins
				time.Sleep(100 * time.Millisecond)
				ctx := context.Background()
				createTopicOnce.Do(func() {
					wins, _ = mockAuctioneer.NewTopic(ctx, core.WinsTopic(from), false)
					proposals, _ = mockAuctioneer.NewTopic(ctx, core.ProposalsTopic(from), false)
				})
				confidential, err := proto.Marshal(&pb.WinningBidConfidential{Sources: cast.SourcesToPb(sources)})
				require.NoError(t, err)
				encrypted, err := encryptKey.Encrypt(confidential)
				require.NoError(t, err)
				msg, err := proto.Marshal(&pb.WinningBid{
					AuctionId: pbid.AuctionId,
					BidId:     bidID,
					Encrypted: encrypted,
				})
				require.NoError(t, err)
				resp, err := wins.Publish(ctx, msg)
				require.NoError(t, err)
				chWinsResponse <- (<-resp).Err
				if sendProposal {
					// send  proposals so bidbot can download the full file, then release the quota.
					msg, err = proto.Marshal(&pb.WinningBidProposal{
						AuctionId:   pbid.AuctionId,
						BidId:       bidID,
						ProposalCid: cid.NewCidV1(cid.Raw, util.Hash([]byte("howdy"))).String(),
					})
					require.NoError(t, err)
					_, err = proposals.Publish(ctx, msg)
					require.NoError(t, err)
				}
			}()
			return []byte(bidID), nil
		})
		_, err = auctions.Publish(ctx, msg, rpc.WithRepublishing(true), rpc.WithIgnoreResponse(true))
		require.NoError(t, err)
		winsResponseError := <-chWinsResponse
		if !expectGoodResponse {
			require.Error(t, winsResponseError,
				fmt.Sprintf("should have responded error for wins in auction %s", auctionID))
			require.Equal(t, core.ErrStringWouldExceedRunningBytesLimit, winsResponseError.Error(),
				fmt.Sprintf("should have responded expected error message in auction %s", auctionID))
		} else {
			require.NoError(t, winsResponseError,
				fmt.Sprintf("should have had not error for wins in auction %s", auctionID))
		}
	}
	t.Run("limit is not hit", func(t *testing.T) { runAuction(t, "auction-1", true, false) })
	t.Run("limit is hit", func(t *testing.T) { runAuction(t, "auction-2", false, false) })
	time.Sleep(service.BidsExpiration)
	t.Run("limit is reset (requested quota for auction-1 is expired), now sends proposal to finish download",
		func(t *testing.T) { runAuction(t, "auction-3", true, true) })
	time.Sleep(service.BidsExpiration)
	t.Run("limit is still hit", func(t *testing.T) { runAuction(t, "auction-4", false, true) })
	time.Sleep(limitPeriod)
	t.Run("limit is cleared after the period", func(t *testing.T) { runAuction(t, "auction-5", true, true) })
}

func validConfig(t *testing.T) (service.Config, txndswrap.TxnDatastore) {
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

	bidParams := service.BidParams{
		WalletAddrSig:         []byte("bar"),
		AskPrice:              100000000000,
		VerifiedAskPrice:      100000000000,
		FastRetrieval:         true,
		DealStartWindow:       oneDayEpochs,
		DealDataFetchAttempts: 3,
		DealDataFetchTimeout:  time.Second,
		DealDataDirectory:     t.TempDir(),
	}
	peerConfig, dir := newPeerConfig(t)
	config := service.Config{
		AuctionFilters: auctionFilters,
		BidParams:      bidParams,
		Peer:           peerConfig,
	}

	store, err := dshelper.NewBadgerTxnDatastore(filepath.Join(dir, "bidstore"))
	require.NoError(t, err)
	return config, store
}

func newPeerConfig(t *testing.T) (rpcpeer.Config, string) {
	dir := t.TempDir()
	priv, _, err := crypto.GenerateEd25519Key(rand.Reader)
	require.NoError(t, err)
	return rpcpeer.Config{
		PrivKey:    priv,
		RepoPath:   dir,
		EnableMDNS: true,
	}, dir
}

func newService(t *testing.T, overrideConfig func(*service.Config)) (*service.Service, error) {
	config, store := validConfig(t)
	if overrideConfig != nil {
		overrideConfig(&config)
	}

	lc := newLotusClientMock()
	fc := newFilClientMock()
	return service.New(config, store, lc, fc)
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
