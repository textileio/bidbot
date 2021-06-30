package limiter

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRunningTotalLimiter(t *testing.T) {
	rl := NewRunningTotalLimiter(50*time.Millisecond, 5)
	require.True(t, rl.Request(3))
	rl.Commit(3)

	time.Sleep(20 * time.Millisecond)
	require.True(t, rl.Request(1))
	// total would become 6
	require.False(t, rl.Request(2))

	time.Sleep(40 * time.Millisecond)
	// 3 will be evicted, and total will become 1 + 2 = 3
	require.True(t, rl.Request(2))
	// total would become 6
	require.False(t, rl.Request(3))
	rl.Withdraw(2)
	// total becomes 1 now, leaves a room for 4
	require.False(t, rl.Request(5))
	require.True(t, rl.Request(4))

	time.Sleep(20 * time.Millisecond)
	// uncommitted tokens never expire
	require.False(t, rl.Request(1))
	rl.Commit(1)

	time.Sleep(60 * time.Millisecond)
	// 1 will be evicted, so we have a room for 1
	require.False(t, rl.Request(2))
	require.True(t, rl.Request(1))

	// if the caller mistakenly commits tokens exceeding the limit, make
	// sure they can be evicted and allow room for new requests.
	rl.Commit(5)
	rl.Commit(5)
	time.Sleep(60 * time.Millisecond)
	require.True(t, rl.Request(5))
}
