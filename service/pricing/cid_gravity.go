package pricing

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	pb "github.com/textileio/bidbot/gen/v1"
	golog "github.com/textileio/go-log/v2"
)

const (
	// loading turns to background after this timeout.
	cidGravityLoadRulesTimeout = 5 * time.Second
	cidGravityCachePeriod      = time.Minute
)

var (
	log              = golog.Logger("rules")
	cidGravityAPIUrl = "https://api.cidgravity.com/api/integration/bidbot"
)

// CIDGravityRules is the format CID gravity API returns. Make public to unmarshal JSON payload. Do not use.
type CIDGravityRules struct {
	Blocked         bool
	MaintenanceMode bool
	PricingRules    []struct {
		Verified    bool
		MinSize     uint64 // bytes
		MaxSize     uint64 // bytes
		MinDuration uint64 // epoches
		MaxDuration uint64 // epoches
		Price       int64  // attoFil
	}
}

type cidGravityRules struct {
	apiKey      string
	clientRules map[string]*clientRules
	lkRules     sync.Mutex
}

// NewCIDGravityRules returns PricingRules based on CID gravity configuration for the storage provider.
func NewCIDGravityRules(apiKey string) PricingRules {
	return &cidGravityRules{apiKey: apiKey}
}

func (cg *cidGravityRules) PricesFor(auction *pb.Auction) (prices ResolvedPrices, valid bool) {
	cg.lkRules.Lock()
	rules, exists := cg.clientRules[auction.ClientAddress]
	if !exists {
		rules = newClientRulesFor(cg.apiKey, auction.ClientAddress)
	}
	cg.clientRules[auction.ClientAddress] = rules
	cg.lkRules.Unlock()
	return rules.PricesFor(auction)
}

type clientRules struct {
	apiKey           string
	clientAddress    string
	rules            atomic.Value // *CIDGravityRules
	rulesLastUpdated atomic.Value // time.Time
	rulesETag        atomic.Value // string
	lkLoadRules      sync.Mutex
}

func newClientRulesFor(apiKey, clientAddress string) *clientRules {
	rules := &clientRules{
		apiKey:        apiKey,
		clientAddress: clientAddress,
	}
	rules.rulesLastUpdated.Store(time.Time{})
	return rules
}

// PricesFor checks the CID gravity rules and returns the resolved prices for the auction. The rules are cached locally
// for some time. It returns valid = false if the rules were never loaded from the CID gravity API.
func (cg *clientRules) PricesFor(auction *pb.Auction) (prices ResolvedPrices, valid bool) {
	cg.maybeReloadRules(cidGravityAPIUrl, cidGravityLoadRulesTimeout, cidGravityCachePeriod)
	rules := cg.rules.Load()
	if rules == nil {
		return
	}
	valid = true
	if rules.(*CIDGravityRules).Blocked {
		return
	}
	if rules.(*CIDGravityRules).MaintenanceMode {
		return
	}
	for _, r := range rules.(*CIDGravityRules).PricingRules {
		if auction.DealSize >= r.MinSize && auction.DealSize < r.MaxSize &&
			auction.DealDuration >= r.MinDuration && auction.DealDuration < r.MaxDuration {
			if r.Verified && !prices.VerifiedPriceValid {
				prices.VerifiedPriceValid, prices.VerifiedPrice = true, r.Price
			} else if !prices.UnverifiedPriceValid {
				prices.UnverifiedPriceValid, prices.UnverifiedPrice = true, r.Price
			}
			if prices.VerifiedPriceValid && prices.UnverifiedPriceValid {
				return
			}
		}
	}
	return
}

// maybeReloadRules reloads rules from the CID gravity API if the cache is not expired. It reloads only once if being
// called concurrently. When loading takes more than the timeout, reloading turns to background and the method returns.
func (cg *clientRules) maybeReloadRules(url string, timeout time.Duration, cachePeriod time.Duration) {
	cg.lkLoadRules.Lock()
	defer cg.lkLoadRules.Unlock()
	lastUpdated := cg.rulesLastUpdated.Load().(time.Time)
	if time.Since(lastUpdated) < cachePeriod {
		return
	}
	chErr := make(chan error, 1)
	go func() {
		chErr <- func() error {
			body := fmt.Sprintf(`{"client_address":"%s"}`, cg.clientAddress)
			req, err := http.NewRequest(http.MethodGet, url, strings.NewReader(body))
			if err != nil {
				return err
			}
			req.Header.Set("Authorization", cg.apiKey)
			etag := cg.rulesETag.Load()
			if etag != nil {
				req.Header.Set("If-None-Match", etag.(string))
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			switch resp.StatusCode {
			case http.StatusOK:
				// proceed
			case http.StatusNotModified:
				return nil
			default:
				return fmt.Errorf("unexpected HTTP status '%v'", resp.Status)
			}
			cg.rulesETag.Store(resp.Header.Get("ETag"))
			defer func() { _ = resp.Body.Close() }()
			b, err := io.ReadAll(resp.Body)
			if err != nil {
				return fmt.Errorf("reading http response: %v", err)
			}
			var rules CIDGravityRules
			if err := json.Unmarshal(b, &rules); err != nil {
				return fmt.Errorf("unmarshalling rules: %v", err)
			}
			cg.rulesLastUpdated.Store(time.Now())
			cg.rules.Store(&rules)
			return nil
		}()
		close(chErr)
	}()
	select {
	case err := <-chErr:
		if err != nil {
			log.Errorf("loading rules: %v", err)
		}
	case <-time.After(timeout):
	}
}
