package pricing

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pb "github.com/textileio/bidbot/gen/v1"
)

func TestIntegrationTest(t *testing.T) {
	t.Parallel()

	auction := &pb.Auction{
		ClientAddress: "0xABCDE",
		DealSize:      1,
		DealDuration:  1,
	}
	apiKey := "<add-here-bidbot-integration-test-api-key>"
	cg := newClientRulesFor("https://staging-api.cidgravity.com/api/integrations/bidbot", apiKey, auction.ClientAddress)

	require.Equal(t, time.Time{}, cg.rulesLastUpdated.Load().(time.Time))
	require.Nil(t, cg.rules.Load())

	err := cg.loadRules(cg.apiURL)
	require.NoError(t, err)
	require.NotEqual(t, time.Time{}, cg.rulesLastUpdated.Load().(time.Time))
	require.NotNil(t, cg.rules.Load())
	rules := cg.rules.Load().(*rawRules)
	require.False(t, rules.Blocked)
	require.False(t, rules.MaintenanceMode)
	require.Equal(t, 11, rules.DealRateLimit)
	require.Equal(t, 0, rules.CurrentDealRate)
	require.Greater(t, len(rules.PricingRules), 0)

	require.Equal(t, "true", rules.PricingRules[0].Verified)
	require.Equal(t, uint64(256), rules.PricingRules[0].MinSize)
	require.Equal(t, uint64(256*1024*1024), rules.PricingRules[0].MaxSize)
	require.Equal(t, uint64(180*24*60*2), rules.PricingRules[0].MinDuration)
	require.Equal(t, uint64(540*24*60*2), rules.PricingRules[0].MaxDuration)
	require.Equal(t, int64(10_000_000_000), rules.PricingRules[0].Price)

	require.Equal(t, "true", rules.PricingRules[1].Verified)
	require.Equal(t, uint64(256*1024*1024), rules.PricingRules[1].MinSize)
	require.Equal(t, uint64(64*1024*1024*1024), rules.PricingRules[1].MaxSize)
	require.Equal(t, uint64(180*24*60*2), rules.PricingRules[1].MinDuration)
	require.Equal(t, uint64(540*24*60*2), rules.PricingRules[1].MaxDuration)
	require.Equal(t, int64(0), rules.PricingRules[1].Price)

	require.Equal(t, "true", rules.PricingRules[2].Verified)
	require.Equal(t, uint64(32*1024*1024), rules.PricingRules[2].MinSize)
	require.Equal(t, uint64(64*1024*1024*1024), rules.PricingRules[2].MaxSize)
	require.Equal(t, uint64(180*24*60*2), rules.PricingRules[2].MinDuration)
	require.Equal(t, uint64(540*24*60*2), rules.PricingRules[2].MaxDuration)
	require.Equal(t, int64(10_000_000_000), rules.PricingRules[2].Price)

}

func TestPriceFor(t *testing.T) {
	cidGravityCachePeriod = time.Second
	rules := &rawRules{
		PricingRules: []struct {
			Verified    string
			MinSize     uint64
			MaxSize     uint64
			MinDuration uint64
			MaxDuration uint64
			Price       int64
		}{
			{
				Verified: "false", Price: 100,
				MinSize: 2 << 20, MaxSize: 2<<30 - 1,
				MinDuration: 1, MaxDuration: 2 << 10,
			},
			{
				Verified: "false", Price: 10,
				MinSize: 2 << 30, MaxSize: 2<<40 - 1,
				MinDuration: 1, MaxDuration: 2 << 10,
			},
			{
				Verified: "false", Price: 1000,
				MinSize: 2 << 30, MaxSize: 2<<40 - 1,
				MinDuration: 2 >> 10, MaxDuration: 2 << 30,
			},
			{
				Verified: "true", Price: 1,
				MinSize: 1, MaxSize: 2<<30 - 1,
				MinDuration: 1, MaxDuration: 2 << 10,
			},
			{
				Verified: "true", Price: 0,
				MinSize: 2 << 30, MaxSize: 2<<40 - 1,
				MinDuration: 1, MaxDuration: 2 << 10,
			},
			{
				Verified: "true", Price: 100,
				MinSize: 2 << 30, MaxSize: 2<<40 - 1,
				MinDuration: 2 >> 10, MaxDuration: 2 << 30,
			},
		},
	}
	auction := &pb.Auction{
		ClientAddress: "fxddd",
		DealSize:      1,
		DealDuration:  1,
	}
	cg := newClientRulesFor("http://localhost:invalid", "key", auction.ClientAddress)

	rp, valid := cg.PricesFor(auction)
	assert.False(t, valid, "prices should be invalid before the rules are loaded")
	assert.False(t, rp.AllowBidding)
	assert.False(t, rp.UnverifiedPriceValid)
	assert.False(t, rp.VerifiedPriceValid)

	cg.rules.Store(rules)
	cg.rulesLastUpdated.Store(time.Now())
	rp, valid = cg.PricesFor(auction)
	assert.True(t, valid)
	assert.True(t, rp.AllowBidding)
	assert.False(t, rp.UnverifiedPriceValid)
	assert.True(t, rp.VerifiedPriceValid)
	assert.Equal(t, int64(1), rp.VerifiedPrice)

	auction.DealSize = 2 << 30
	rp, valid = cg.PricesFor(auction)
	assert.True(t, valid)
	assert.True(t, rp.AllowBidding)
	assert.True(t, rp.UnverifiedPriceValid)
	assert.Equal(t, int64(10), rp.UnverifiedPrice)
	assert.True(t, rp.VerifiedPriceValid)
	assert.Equal(t, int64(0), rp.VerifiedPrice)

	auction.DealDuration = 2 << 20
	rp, valid = cg.PricesFor(auction)
	assert.True(t, valid)
	assert.True(t, rp.AllowBidding)
	assert.True(t, rp.UnverifiedPriceValid)
	assert.Equal(t, int64(1000), rp.UnverifiedPrice)
	assert.True(t, rp.VerifiedPriceValid)
	assert.Equal(t, int64(100), rp.VerifiedPrice)

	// start testing other flags like deal rate, blocked etc
	rules.DealRateLimit = 100
	rp, valid = cg.PricesFor(auction)
	assert.True(t, valid)
	assert.True(t, rp.AllowBidding)
	assert.True(t, rp.UnverifiedPriceValid)
	assert.True(t, rp.VerifiedPriceValid)

	rules.CurrentDealRate = rules.DealRateLimit
	rp, valid = cg.PricesFor(auction)
	assert.True(t, valid)
	assert.False(t, rp.AllowBidding)
	assert.False(t, rp.UnverifiedPriceValid)
	assert.False(t, rp.VerifiedPriceValid)

	rules.CurrentDealRate = 0
	rules.MaintenanceMode = true
	rp, valid = cg.PricesFor(auction)
	assert.True(t, valid)
	assert.False(t, rp.AllowBidding)
	assert.False(t, rp.UnverifiedPriceValid)
	assert.False(t, rp.VerifiedPriceValid)

	rules.MaintenanceMode = false
	rules.Blocked = true
	rp, valid = cg.PricesFor(auction)
	assert.True(t, valid)
	assert.False(t, rp.AllowBidding)
	assert.False(t, rp.UnverifiedPriceValid)
	assert.False(t, rp.VerifiedPriceValid)

	time.Sleep(cidGravityCachePeriod)
	rp, valid = cg.PricesFor(auction)
	assert.False(t, valid, "prices should be invalid when the rules expire")
}

func TestMaybeReloadRules(t *testing.T) {
	cg := newClientRulesFor("http://localhost:invalid", "key", "pk")
	apiResponse := []byte(`{
	     "pricingRules": [
		     {
		          "verified": "false",
		          "minSize": 1,
		          "maxSize": 2,
		          "minDuration": 1,
		          "maxDuration": 2,
		          "price": 0
		     }
		 ],
		"blocked": false,
		"maintenanceMode": false,
		"dealRateLimit": 50,
		"currentDealRate": 0
    }`)
	for _, testCase := range []struct {
		name         string
		cachePeriod  time.Duration
		etag         string
		called       bool
		fullResponse bool
	}{
		{"first time", 100 * time.Millisecond, "v1", true, true},
		{"cached", 100 * time.Millisecond, "v1", false, false},
		{"cache expired", 0, "v1", true, false},
		{"changed", 0, "v2", true, true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var called, fullResponse bool
			server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
				called = true
				assert.Equal(t, "key", req.Header.Get("Authorization"))
				b, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				params := make(map[string]string)
				require.NoError(t, json.Unmarshal(b, &params))
				assert.Equal(t, "pk", params["clientAddress"])
				if req.Header.Get("If-None-Match") == testCase.etag {
					rw.WriteHeader(http.StatusNotModified)
				} else {
					fullResponse = true
					rw.Header().Set("ETag", testCase.etag)
					_, _ = rw.Write(apiResponse)
				}
			}))
			cg.maybeReloadRules(server.URL, 100*time.Millisecond, testCase.cachePeriod)
			require.Equal(t, testCase.called, called)
			require.Equal(t, testCase.fullResponse, fullResponse)
		})
	}
	t.Run("timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			time.Sleep(100 * time.Millisecond)
			response, _ := json.Marshal(rawRules{MaintenanceMode: true})
			_, _ = rw.Write(response)
		}))
		cg.maybeReloadRules(server.URL, 10*time.Millisecond, 0)
		rules := cg.rules.Load().(*rawRules)
		assert.False(t, rules.MaintenanceMode, "should have loaded the cached rules")
		assert.Len(t, rules.PricingRules, 1)
		assert.Equal(t, uint64(1), rules.PricingRules[0].MinDuration)
		time.Sleep(100 * time.Millisecond)
		assert.True(t,
			cg.rules.Load().(*rawRules).MaintenanceMode,
			"should have loaded the new rules")
	})
	t.Run("API rate limit", func(t *testing.T) {
		reqs := 0
		backoff429 = time.Millisecond * 50
		server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
			if reqs == 0 {
				rw.WriteHeader(http.StatusTooManyRequests)
			} else {
				response, _ := json.Marshal(rawRules{MaintenanceMode: true})
				_, _ = rw.Write(response)
			}
			reqs++
		}))
		timeout := time.Millisecond
		valid := cg.maybeReloadRules(server.URL, timeout, 0)
		require.False(t, valid, "limit is hit")

		require.Eventually(t, func() bool {
			valid = cg.maybeReloadRules(server.URL, timeout, 0)
			return !valid
		}, time.Second, time.Millisecond*10)

		require.Eventually(t, func() bool {
			valid = cg.maybeReloadRules(server.URL, timeout, 0)
			return valid
		}, time.Second*10, time.Second)
	})
}
