package limiter

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRequestCommit(t *testing.T) {
	longExp := time.Minute
	rl := NewRunningTotalLimiter(5, 50*time.Millisecond)
	require.True(t, rl.Request("id1", 3, longExp))
	rl.Commit("id1")

	time.Sleep(20 * time.Millisecond)
	require.True(t, rl.Request("id2", 1, longExp))
	require.False(t, rl.Request("id3", 2, longExp))

	time.Sleep(40 * time.Millisecond)
	// id1 will be evicted, and total will become 1 + 2 = 3
	require.True(t, rl.Request("id4", 2, longExp))
	// total would become 6
	require.False(t, rl.Request("id5", 3, longExp))
	rl.Withdraw("id4")
	// total would become 1, leaves a room only for 4
	require.False(t, rl.Request("id6", 5, longExp))
	require.True(t, rl.Request("id7", 4, longExp))

	time.Sleep(20 * time.Millisecond)
	// uncommitted tokens id2 and id7 never expire
	require.False(t, rl.Request("id8", 1, longExp))
	rl.Commit("id2") // 1

	time.Sleep(60 * time.Millisecond)
	// id2 would be evicted, so we have a room for 1
	require.False(t, rl.Request("id9", 2, longExp))
	require.True(t, rl.Request("id10", 1, longExp))

	// committing tokens more than once has no damage.
	rl.Commit("id7")
	rl.Commit("id7")
	time.Sleep(60 * time.Millisecond)
	require.True(t, rl.Request("id11", 4, time.Millisecond))
}

func TestSecure(t *testing.T) {
	shortExp := 10 * time.Millisecond
	rl := NewRunningTotalLimiter(5, 50*time.Millisecond)
	require.True(t, rl.Request("id1", 3, shortExp))
	require.False(t, rl.Request("id2", 3, shortExp))
	time.Sleep(2 * shortExp)
	// id1 expires at this point
	require.True(t, rl.Request("id2", 3, shortExp))
	rl.Secure("id2")
	time.Sleep(2 * shortExp)
	// id2 was secured, so it does not expire at this point
	require.False(t, rl.Request("id3", 3, shortExp))
	require.True(t, rl.Request("id3", 1, shortExp))
	rl.Withdraw("id2")
	rl.Withdraw("id3")
	// withdraw works to both secured and not-yet-expired tokens.
	require.True(t, rl.Request("id4", 5, shortExp))
}
