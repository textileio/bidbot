package filclient_test

import (
	"encoding/hex"
	"testing"

	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	. "github.com/textileio/bidbot/lib/filclient"
)

const (
	lotusGatewayURL = "https://api.node.glif.io"
	bidderSig       = "0116acc1675a21bb4a1355a2ebceb24fa5bea612fd5b88fb6894f3139eae73f6142121ce6c1c837cc45022abdcb49dca" +
		"77435a41d30ca8ef613aa4d50f1707538001"
	bidderID = "002408011220bc22df3d9a3a8202ddf6e3dbf7cb07c872dc2e024737318a223e275bd06c1281"
)

func TestFilClient_GetChainHeight(t *testing.T) {
	t.Parallel()
	client, err := New(lotusGatewayURL, true)
	require.NoError(t, err)

	height, err := client.GetChainHeight()
	require.NoError(t, err)
	assert.Greater(t, height, uint64(0))
}

// This test is somewhat unneeded now, leaving it for the record.
// Running in fake-mode isn't basically doing anything.
func TestFilClient_VerifyBidder(t *testing.T) {
	t.Parallel()
	client, err := New(lotusGatewayURL, true)
	require.NoError(t, err)

	sigBytes, err := hex.DecodeString(bidderSig)
	require.NoError(t, err)
	pidBytes, err := hex.DecodeString(bidderID)
	require.NoError(t, err)
	pid, err := peer.IDFromBytes(pidBytes)
	require.NoError(t, err)

	ok, err := client.VerifyBidder(sigBytes, pid, "fakeModeON")
	require.NoError(t, err)
	assert.True(t, ok)
}
