package apitest

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strconv"

	"github.com/google/uuid"
	"github.com/ipfs/go-cid"
	cbor "github.com/ipfs/go-ipld-cbor"
	format "github.com/ipfs/go-ipld-format"
	"github.com/ipld/go-car"
	"github.com/multiformats/go-multihash"
	"github.com/textileio/bidbot/lib/auction"
)

// DataURIHTTPGateway is a test http server for bidbot data uris.
type DataURIHTTPGateway struct {
	server *httptest.Server
	data   map[cid.Cid][]byte
	dag    format.DAGService
}

// NewDataURIHTTPGateway returns a new *DataURIHTTPGateway for testing.
func NewDataURIHTTPGateway(dag format.DAGService) *DataURIHTTPGateway {
	g := &DataURIHTTPGateway{
		data: make(map[cid.Cid][]byte),
		dag:  dag,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/cid/", handler(g))
	g.server = httptest.NewServer(mux)
	return g
}

// Close it.
func (g *DataURIHTTPGateway) Close() {
	g.server.Close()
}

// CreateHTTPSources creates a uri and wraps it into auction.Sources.
func (g *DataURIHTTPGateway) CreateHTTPSources(serve bool) (cid.Cid, auction.Sources, error) {
	cid, s, err := g.CreateURI(serve)
	if err != nil {
		return cid, auction.Sources{}, err
	}
	u, _ := url.Parse(s)
	return cid, auction.Sources{
		CARURL: &auction.CARURL{
			URL: *u,
		},
	}, nil
}

// CreateURI creates a uri that will be served over the gateway if serve is true.
func (g *DataURIHTTPGateway) CreateURI(serve bool) (cid.Cid, string, error) {
	node, err := cbor.WrapObject([]byte(uuid.NewString()), multihash.SHA2_256, -1)
	if err != nil {
		return cid.Cid{}, "", err
	}
	if err := g.dag.Add(context.Background(), node); err != nil {
		return cid.Cid{}, "", err
	}
	buff := &bytes.Buffer{}
	if err := car.WriteCar(context.Background(), g.dag, []cid.Cid{node.Cid()}, buff); err != nil {
		return cid.Cid{}, "", err
	}

	if serve {
		g.data[node.Cid()] = buff.Bytes()
	}

	return node.Cid(), fmt.Sprintf("%s/cid/%s", g.server.URL, node.Cid()), nil
}

// CreateURIWithWrongRoot creates a uri whos cid points to a car file with the wrong root.
func (g *DataURIHTTPGateway) CreateURIWithWrongRoot() (cid.Cid, string, error) {
	c1, _, err := g.CreateURI(true)
	if err != nil {
		return cid.Cid{}, "", err
	}
	c2, uri2, err := g.CreateURI(false)
	if err != nil {
		return cid.Cid{}, "", err
	}
	// Swap data so that c2 points to c1's data
	g.data[c2] = g.data[c1]
	delete(g.data, c1)

	return c2, uri2, nil
}

func handler(g *DataURIHTTPGateway) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := cid.Decode(path.Base(r.URL.Path))
		errCheck(err)
		if data, ok := g.data[c]; ok {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(c.String())+".car")
			_, err = w.Write(data)
			errCheck(err)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func errCheck(err error) {
	if err != nil {
		panic(err)
	}
}
