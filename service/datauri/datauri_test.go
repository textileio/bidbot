package datauri_test

import (
	"context"
	"testing"

	format "github.com/ipfs/go-ipld-format"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/textileio/bidbot/lib/finalizer"
	"github.com/textileio/bidbot/lib/marketpeer"
	. "github.com/textileio/bidbot/service/datauri"
	"github.com/textileio/bidbot/service/datauri/apitest"
)

var testCid = "bafybeic6xu6afw5lg6a6h6uk27twq3bmzxjg346nhsyenuhxwzfv6yhu5y"

func TestNewURI(t *testing.T) {
	// http
	u, err := NewURI(testCid, "http://foo.com/cid/"+testCid)
	require.NoError(t, err)
	assert.Equal(t, testCid, u.Cid().String())
	// https
	_, err = NewURI(testCid, "https://foo.com/cid/"+testCid)
	require.NoError(t, err)
	// not supported
	_, err = NewURI(testCid, "s3://foo.com/notsupported")
	require.ErrorIs(t, err, ErrSchemeNotSupported)
	// no cid in url
	_, err = NewURI(testCid, "https://foo.com/123")
	require.NoError(t, err)

	// invalid cid
	_, err = NewURI("malformed-cid", "https://foo.com/123")
	require.Error(t, err)
	_, err = NewURI("", "https://foo.com/123")
	require.Error(t, err)
}

func TestURI_Validate(t *testing.T) {
	gw := apitest.NewDataURIHTTPGateway(createDagService(t))
	t.Cleanup(gw.Close)

	// Validate good car file
	payloadCid, dataURI, err := gw.CreateURI(true)
	require.NoError(t, err)
	u, err := NewURI(payloadCid.String(), dataURI)
	require.NoError(t, err)
	err = u.Validate(context.Background())
	require.NoError(t, err)

	// Validate car file not found
	payloadCid, dataURI, err = gw.CreateURI(false)
	require.NoError(t, err)
	u, err = NewURI(payloadCid.String(), dataURI)
	require.NoError(t, err)
	err = u.Validate(context.Background())
	require.ErrorIs(t, err, ErrCarFileUnavailable)

	// Validate bad car file
	payloadCid, dataURI, err = gw.CreateURIWithWrongRoot()
	require.NoError(t, err)
	u, err = NewURI(payloadCid.String(), dataURI)
	require.NoError(t, err)
	err = u.Validate(context.Background())
	require.ErrorIs(t, err, ErrInvalidCarFile)
}

func createDagService(t *testing.T) format.DAGService {
	dir := t.TempDir()
	fin := finalizer.NewFinalizer()
	t.Cleanup(func() {
		require.NoError(t, fin.Cleanup(nil))
	})
	p, err := marketpeer.New(marketpeer.Config{RepoPath: dir})
	require.NoError(t, err)
	fin.Add(p)
	return p.DAGService()
}
