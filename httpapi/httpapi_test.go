package httpapi

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/libp2p/go-libp2p-core/crypto"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/textileio/bidbot/lib/broker"
	"github.com/textileio/bidbot/lib/marketpeer"
	"github.com/textileio/bidbot/service/datauri"
	bidstore "github.com/textileio/bidbot/service/store"
	golog "github.com/textileio/go-log/v2"
)

func init() {
	golog.SetAllLoggers(golog.LevelDebug)
}

func TestAPI_Deals(t *testing.T) {
	// auctioneerID has to be populated to avoid unmarshal error
	sk, _, err := crypto.GenerateEd25519Key(rand.Reader)
	require.NoError(t, err)
	auctioneerID, err := peer.IDFromPrivateKey(sk)
	require.NoError(t, err)

	unspecified := &bidstore.Bid{AuctioneerID: auctioneerID}
	submitted := &bidstore.Bid{ID: "a", Status: bidstore.BidStatusSubmitted, AuctioneerID: auctioneerID}
	queued := &bidstore.Bid{ID: "b", Status: bidstore.BidStatusQueuedData, AuctioneerID: auctioneerID}
	finalized := &bidstore.Bid{ID: "c", Status: bidstore.BidStatusFinalized, AuctioneerID: auctioneerID}
	allBids := []*bidstore.Bid{unspecified, queued, submitted, finalized}

	for _, tc := range []struct {
		name               string
		fullList           []*bidstore.Bid
		url                string
		expectedStatusCode int
		expectedResult     []*bidstore.Bid
	}{
		// path and query handling
		{"no filter", nil, "/deals", http.StatusOK, nil},
		{"no filter with trailing slash", nil, "/deals/", http.StatusOK, nil},
		{"empty filter", nil, "/deals?status=", http.StatusOK, nil},
		{"invalid filter", nil, "/deals?status=abc", http.StatusBadRequest, nil},
		{"multiple with invalid filter", nil, "/deals?status=awaiting_proposal, abc", http.StatusBadRequest, nil},
		{"valid filter", nil, "/deals?status=awaiting_proposal", http.StatusOK, nil},
		{"multiple valid filter", nil, "/deals?status= awaiting_proposal,  queued_data", http.StatusOK, nil},
		{"duplicated filter", nil, "/deals?status= awaiting_proposal,  queued_data, awaiting_proposal", http.StatusOK, nil},

		// list deals
		{"all", allBids, "/deals", http.StatusOK, allBids},
		{"all with filter", allBids, "/deals?status= awaiting_proposal,  queued_data, awaiting_proposal",
			http.StatusOK, []*bidstore.Bid{queued}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ms := &mockService{}
			mux := createMux(ms)
			ms.On("ListBids", mock.Anything).Return(tc.fullList, nil)
			res := httptest.NewRecorder()
			req, _ := http.NewRequest(http.MethodGet, tc.url, nil)
			mux.ServeHTTP(res, req)
			require.Equal(t, tc.expectedStatusCode, res.Code)
			if tc.expectedStatusCode == http.StatusOK {
				var bids []*bidstore.Bid
				require.NoError(t, json.Unmarshal(res.Body.Bytes(), &bids))
				require.Equal(t, tc.expectedResult, bids)
			}
		})
	}

	ms := &mockService{}
	mux := createMux(ms)
	ms.On("GetBid", broker.BidID("a")).Return(submitted, nil)
	ms.On("GetBid", broker.BidID("z")).Return(nil, bidstore.ErrBidNotFound)
	for _, tc := range []struct {
		name           string
		url            string
		expectedResult []*bidstore.Bid
	}{
		{"get deal found", "/deals/a", []*bidstore.Bid{submitted}},
		{"get deal not found", "/deals/z", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := httptest.NewRecorder()
			req, _ := http.NewRequest(http.MethodGet, tc.url, nil)
			mux.ServeHTTP(res, req)
			require.Equal(t, http.StatusOK, res.Code)
			var bids []*bidstore.Bid
			require.NoError(t, json.Unmarshal(res.Body.Bytes(), &bids))
			require.Equal(t, tc.expectedResult, bids)
		})
	}
}

func TestAPI_WriteDataURI(t *testing.T) {
	ms := &mockService{}
	mux := createMux(ms)
	cid := "QmdKDf5nepPLXErXd1pYY8hA82yjMaW3fdkU8D8kiz3jH1"
	cid2 := "QmdKDf5nepPLXErXd1pYY8hA82yjMaW3fdkU8D8kiz3jH2"
	uri1 := "s3://foo.com/cid/123"
	uri2 := "https://foo.com/abc"
	ms.On("WriteDataURI", cid, uri1).Return("", datauri.ErrSchemeNotSupported)
	ms.On("WriteDataURI", cid, uri2).Return("foo/bar", nil)
	ms.On("WriteDataURI", cid2, uri2).Return("", errors.New("some error"))
	for _, tc := range []struct {
		name               string
		url                string
		expectedStatusCode int
		expectedResult     string
	}{
		{"unsupported scheme", "/datauri?uri=s3%3A%2F%2Ffoo.com%2Fcid%2F123", http.StatusBadRequest, ""},
		{
			"success",
			"/datauri?uri=https%3A%2F%2Ffoo.com%2Fabc&cid=QmdKDf5nepPLXErXd1pYY8hA82yjMaW3fdkU8D8kiz3jH1",
			http.StatusOK,
			"wrote to foo/bar",
		},
		{
			"success with trailing slash",
			"/datauri/?uri=https%3A%2F%2Ffoo.com%2Fabc&cid=QmdKDf5nepPLXErXd1pYY8hA82yjMaW3fdkU8D8kiz3jH1",
			http.StatusOK,
			"wrote to foo/bar",
		},
		{
			"error",
			"/datauri?uri=https%3A%2F%2Ffoo.com%2Fabc&cid=QmdKDf5nepPLXErXd1pYY8hA82yjMaW3fdkU8D8kiz3jH2",
			http.StatusInternalServerError,
			"",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := httptest.NewRecorder()
			req, _ := http.NewRequest(http.MethodGet, tc.url, nil)
			mux.ServeHTTP(res, req)
			require.Equal(t, tc.expectedStatusCode, res.Code)
			if tc.expectedStatusCode == http.StatusOK {
				require.Equal(t, tc.expectedResult, res.Body.String())
			}
		})
	}
}

type mockService struct {
	mock.Mock
}

func (s *mockService) PeerInfo() (*marketpeer.PeerInfo, error) {
	panic("not implemented")
}

func (s *mockService) GetBid(id broker.BidID) (*bidstore.Bid, error) {
	args := s.Called(id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*bidstore.Bid), args.Error(1)
}

func (s *mockService) ListBids(query bidstore.Query) ([]*bidstore.Bid, error) {
	args := s.Called(query)
	return args.Get(0).([]*bidstore.Bid), args.Error(1)
}

func (s *mockService) WriteDataURI(payloadCid, uri string) (string, error) {
	args := s.Called(payloadCid, uri)
	return args.String(0), args.Error(1)
}
