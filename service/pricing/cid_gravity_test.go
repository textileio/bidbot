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

func TestPriceFor(t *testing.T) {
	cidGravityAPIUrl = "http://localhost:invalid" // do not care about rules loading
	rules := &CIDGravityRules{
		PricingRules: []struct {
			Verified    bool
			MinSize     uint64
			MaxSize     uint64
			MinDuration uint64
			MaxDuration uint64
			Price       int64
		}{
			{
				Verified: false, Price: 100,
				MinSize: 2 << 20, MaxSize: 2 << 30,
				MinDuration: 1, MaxDuration: 2 << 10,
			},
			{
				Verified: false, Price: 10,
				MinSize: 2 << 30, MaxSize: 2 << 40,
				MinDuration: 1, MaxDuration: 2 << 10,
			},
			{
				Verified: false, Price: 1000,
				MinSize: 2 << 30, MaxSize: 2 << 40,
				MinDuration: 2 >> 10, MaxDuration: 2 << 30,
			},
			{
				Verified: true, Price: 1,
				MinSize: 1, MaxSize: 2 << 30,
				MinDuration: 1, MaxDuration: 2 << 10,
			},
			{
				Verified: true, Price: 0,
				MinSize: 2 << 30, MaxSize: 2 << 40,
				MinDuration: 1, MaxDuration: 2 << 10,
			},
			{
				Verified: true, Price: 100,
				MinSize: 2 << 30, MaxSize: 2 << 40,
				MinDuration: 2 >> 10, MaxDuration: 2 << 30,
			},
		},
	}
	auction := &pb.Auction{
		ClientAddress: "fxddd",
		DealSize:      1,
		DealDuration:  1,
	}
	cg := newClientRulesFor("key", auction.ClientAddress)

	rp, valid := cg.PricesFor(auction)
	assert.False(t, valid)
	assert.False(t, rp.UnverifiedPriceValid)
	assert.False(t, rp.VerifiedPriceValid)

	cg.rules.Store(rules)
	rp, valid = cg.PricesFor(auction)
	assert.True(t, valid)
	assert.False(t, rp.UnverifiedPriceValid)
	assert.True(t, rp.VerifiedPriceValid)
	assert.Equal(t, int64(1), rp.VerifiedPrice)

	auction.DealSize = 2 << 30
	rp, valid = cg.PricesFor(auction)
	assert.True(t, valid)
	assert.True(t, rp.UnverifiedPriceValid)
	assert.Equal(t, int64(10), rp.UnverifiedPrice)
	assert.True(t, rp.VerifiedPriceValid)
	assert.Equal(t, int64(0), rp.VerifiedPrice)

	auction.DealDuration = 2 << 20
	rp, valid = cg.PricesFor(auction)
	assert.True(t, valid)
	assert.True(t, rp.UnverifiedPriceValid)
	assert.Equal(t, int64(1000), rp.UnverifiedPrice)
	assert.True(t, rp.VerifiedPriceValid)
	assert.Equal(t, int64(100), rp.VerifiedPrice)

	rules.MaintenanceMode = true
	rp, valid = cg.PricesFor(auction)
	assert.True(t, valid)
	assert.False(t, rp.UnverifiedPriceValid)
	assert.False(t, rp.VerifiedPriceValid)

	rules.MaintenanceMode = false
	rules.Blocked = true
	rp, valid = cg.PricesFor(auction)
	assert.True(t, valid)
	assert.False(t, rp.UnverifiedPriceValid)
	assert.False(t, rp.VerifiedPriceValid)
}

func TestMaybeReloadRules(t *testing.T) {
	cg := newClientRulesFor("key", "pk")
	apiResponse := []byte(`{
	     "pricingRules": [
		     {
		          "verified": false,
		          "minSize": 1,
		          "maxSize": 2,
		          "minDuration": 1,
		          "maxDuration": 2,
		          "price": 0
		     }
		 ],
		"blocked": false,
		"maintenanceMode": false
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
			response, _ := json.Marshal(CIDGravityRules{MaintenanceMode: true})
			_, _ = rw.Write(response)
		}))
		cg.maybeReloadRules(server.URL, 10*time.Millisecond, 0)
		rules := cg.rules.Load().(*CIDGravityRules)
		assert.False(t, rules.MaintenanceMode, "should have loaded the cached rules")
		assert.Len(t, rules.PricingRules, 1)
		assert.Equal(t, uint64(1), rules.PricingRules[0].MinDuration)
		time.Sleep(100 * time.Millisecond)
		assert.True(t,
			cg.rules.Load().(*CIDGravityRules).MaintenanceMode,
			"should have loaded the new rules")
	})
}
