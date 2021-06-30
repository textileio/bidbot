package limiter

import (
	"container/list"
	"sync"
	"time"
)

// Limiter is the interface for RunningTotalLimiter. It's there just so we can
// have a NopeLimiter which does nothing.
type Limiter interface {
	Request(n uint64) bool
	Commit(n uint64)
	Withdraw(n uint64)
}

// RunningTotalLimiter is a variant of sliding log rate limiter. It keeps all
// requests with timestamps. What makes it different from a typical rate limiter is
// that there are two phases - The caller first requests for 'n' tokens, does
// some work, then either commits the tokens, or withdraws them so they can be
// requested by others. So at anytime, the running total represents the tokens
// committed in that period of time, plus those requested but not committed yet.
//
// If we are going to have millions of requests, we should switch to a sliding
// window implementation like https://github.com/RussellLuo/slidingwindow,
// which saves space with the cost of some inaccuracy.
type RunningTotalLimiter struct {
	period time.Duration
	limit  uint64
	total  uint64
	list   *list.List // list keeps committed tokens chronologically
	mu     sync.Mutex
}

type elem struct {
	ts time.Time
	n  uint64
}

// NewRunningTotalLimiter creates a Limiter which caps the running total in the 'period' to 'limit'.
func NewRunningTotalLimiter(period time.Duration, limit uint64) Limiter {
	return &RunningTotalLimiter{period: period, limit: limit, list: list.New()}
}

// Request reqeusts for 'n' tokens and returns if granted or not. If granted,
// the tokens must be either withdrawed or committed some time later, or we'll
// run out of tokens.
func (rl *RunningTotalLimiter) Request(n uint64) bool {
	now := time.Now()
	rl.mu.Lock()
	defer rl.mu.Unlock()
	for e := rl.list.Front(); e != nil; {
		item := e.Value.(elem)
		if now.Sub(item.ts) <= rl.period {
			break
		}
		if rl.total < item.n {
			// if the caller commits some tokens more than once,
			// this could happen. Add a guard here just to prevent
			// the total from wraping around to a gigantic number.
			rl.total = 0
		} else {
			rl.total -= item.n
		}
		toRemove := e
		e = e.Next()
		rl.list.Remove(toRemove)
	}
	if rl.total+n > rl.limit {
		return false
	}
	rl.total += n
	return true
}

// Commit makes the requested n tokens permanent for the configured period.
// It's the caller's responsibility to always request the tokens before
// committing them.
func (rl *RunningTotalLimiter) Commit(n uint64) {
	e := elem{time.Now(), n}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.list.PushBack(e)
}

// Withdraw withdraws 'n' grant previously requested.
func (rl *RunningTotalLimiter) Withdraw(n uint64) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if rl.total < n {
		panic("total would become negative. are you attempting to withdraw more than once?")
	}
	rl.total -= n
}

// NopeLimiter does no limit.
type NopeLimiter struct{}

// Request always return true.
func (l NopeLimiter) Request(n uint64) bool { return true }

// Commit does nothing.
func (l NopeLimiter) Commit(n uint64) {}

// Withdraw does nothing.
func (l NopeLimiter) Withdraw(n uint64) {}
