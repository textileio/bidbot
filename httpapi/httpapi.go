package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/textileio/bidbot/buildinfo"
	"github.com/textileio/bidbot/lib/auction"
	"github.com/textileio/bidbot/lib/datauri"
	bidstore "github.com/textileio/bidbot/service/store"
	"github.com/textileio/go-libp2p-pubsub-rpc/peer"
	golog "github.com/textileio/go-log/v2"
)

var (
	log = golog.Logger("bidbot/api")
)

// Service provides scoped access to the bidbot service.
type Service interface {
	PeerInfo() (*peer.Info, error)
	ListBids(query bidstore.Query) ([]*bidstore.Bid, error)
	GetBid(ctx context.Context, id auction.BidID) (*bidstore.Bid, error)
	WriteDataURI(payloadCid, uri string) (string, error)
	SetPaused(paused bool)
}

// NewServer returns a new http server for bidbot commands.
func NewServer(listenAddr string, service Service) (*http.Server, error) {
	httpServer := &http.Server{
		Addr:    listenAddr,
		Handler: createMux(service),
	}

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Errorf("stopping http server: %s", err)
		}
	}()

	log.Infof("http server started at %s", listenAddr)
	return httpServer, nil
}

func createMux(service Service) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", getOnly(healthHandler))
	mux.HandleFunc("/id", getOnly(idHandler(service)))
	mux.HandleFunc("/pause", putOnly(pauseHandler(service)))
	mux.HandleFunc("/resume", putOnly(resumeHandler(service)))
	mux.HandleFunc("/version", getOnly(versionHandler))
	// allow both with and without trailing slash
	deals := getOnly(dealsHandler(service))
	mux.HandleFunc("/deals", deals)
	mux.HandleFunc("/deals/", deals)
	dataURI := getOnly(dataURIRequestHandler(service))
	mux.HandleFunc("/datauri", dataURI)
	mux.HandleFunc("/datauri/", dataURI)
	return mux
}

func getOnly(f http.HandlerFunc) http.HandlerFunc {
	return methodOnly(http.MethodGet, f)
}

func putOnly(f http.HandlerFunc) http.HandlerFunc {
	return methodOnly(http.MethodPut, f)
}

func methodOnly(method string, f http.HandlerFunc) http.HandlerFunc {
	msg := fmt.Sprintf("only %s method is allowed", method)
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			httpError(w, msg, http.StatusBadRequest)
			return
		}
		f(w, r)
	}
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func idHandler(service Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		info, err := service.PeerInfo()
		if err != nil {
			httpError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		data, err := json.MarshalIndent(info, "", "\t")
		if err != nil {
			httpError(w, fmt.Sprintf("marshaling id: %s", err), http.StatusInternalServerError)
			return
		}
		_, err = w.Write(data)
		if err != nil {
			log.Errorf("write failed: %v", err)
		}
	}
}

func pauseHandler(service Service) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		service.SetPaused(true)
		w.WriteHeader(http.StatusOK)
	}
}

func resumeHandler(service Service) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		service.SetPaused(false)
		w.WriteHeader(http.StatusOK)
	}
}

func versionHandler(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte(buildinfo.Summary()))
}

func dealsHandler(service Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var bids []*bidstore.Bid
		urlParts := strings.SplitN(r.URL.Path, "/", 3)
		if len(urlParts) < 3 || urlParts[2] == "" {
			statusFilters := strings.Split(r.URL.Query().Get("status"), ",")
			var proceed bool
			if bids, proceed = listBids(w, service, statusFilters); !proceed {
				return
			}
		} else {
			bid, err := service.GetBid(r.Context(), auction.BidID(urlParts[2]))
			if err == nil {
				bids = append(bids, bid)
			} else if err != bidstore.ErrBidNotFound {
				httpError(w, fmt.Sprintf("get bid: %s", err), http.StatusInternalServerError)
				return
			}
		}
		data, err := json.Marshal(bids)
		if err != nil {
			httpError(w, fmt.Sprintf("json encoding: %s", err), http.StatusInternalServerError)
			return
		}
		_, err = w.Write(data)
		if err != nil {
			log.Errorf("write failed: %v", err)
		}
	}
}

func listBids(w http.ResponseWriter, service Service, statusFilters []string) (bids []*bidstore.Bid, proceed bool) {
	var filters []bidstore.BidStatus
	for _, s := range statusFilters {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		bs, err := bidstore.BidStatusByString(s)
		if err != nil {
			httpError(w, fmt.Sprintf("%s: %s", s, err), http.StatusBadRequest)
			return nil, false
		}
		filters = append(filters, bs)
	}
	// for simplicity we apply filters after retrieving. if performance
	// becomes an issue, we can add query filters.
	fullList, err := service.ListBids(bidstore.Query{Limit: -1})
	if err != nil {
		httpError(w, fmt.Sprintf("listing bids: %s", err), http.StatusInternalServerError)
		return nil, false
	}
	if len(filters) == 0 {
		return fullList, true
	}
	for _, bid := range fullList {
		for _, status := range filters {
			if bid.Status == status {
				bids = append(bids, bid)
				break
			}
		}
	}
	return bids, true
}

func dataURIRequestHandler(service Service) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		getQueryParam := func(key string) (string, string) {
			query := r.URL.Query().Get(key)
			if query == "" {
				return "", fmt.Sprintf("missing '%s' query param", key)
			}
			raw, err := url.QueryUnescape(query)
			if err != nil {
				return "", fmt.Sprintf("parsing '%s': %s", key, err)
			}
			return raw, ""
		}
		uri, errstring := getQueryParam("uri")
		if errstring != "" {
			httpError(w, errstring, http.StatusBadRequest)
			return
		}
		payloadCid, errstring := getQueryParam("cid")
		if errstring != "" {
			httpError(w, errstring, http.StatusBadRequest)
			return
		}
		dest, err := service.WriteDataURI(payloadCid, uri)
		if errors.Is(err, datauri.ErrSchemeNotSupported) ||
			errors.Is(err, datauri.ErrCarFileUnavailable) ||
			errors.Is(err, datauri.ErrInvalidCarFile) {
			httpError(w, fmt.Sprintf("writing data uri: %s", err), http.StatusBadRequest)
			return
		} else if err != nil {
			httpError(w, fmt.Sprintf("writing data uri: %s", err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		resp := fmt.Sprintf("wrote to %s", dest)
		if _, err = w.Write([]byte(resp)); err != nil {
			httpError(w, fmt.Sprintf("writing response: %s", err), http.StatusInternalServerError)
			return
		}
	}
}

func httpError(w http.ResponseWriter, err string, status int) {
	log.Debugf("request error: %s", err)
	http.Error(w, err, status)
}
