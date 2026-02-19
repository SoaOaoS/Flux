package strategy_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"golb/internal/strategy"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func makeBackend(t *testing.T, rawURL string, weight int) *strategy.Backend {
	t.Helper()
	b, err := strategy.NewBackend(rawURL, weight)
	require.NoError(t, err)
	return b
}

// countDistribution calls picker.Next() n times (calling Done after each) and
// returns a map[RawURL]count.
func countDistribution(t *testing.T, p strategy.Picker, n int) map[string]int {
	t.Helper()
	counts := map[string]int{}
	for i := 0; i < n; i++ {
		b, err := p.Next()
		require.NoError(t, err)
		p.Done(b)
		counts[b.RawURL]++
	}
	return counts
}

// ── RoundRobin ───────────────────────────────────────────────────────────────

func TestRoundRobin_EvenDistribution(t *testing.T) {
	b1 := makeBackend(t, "http://b1:80", 1)
	b2 := makeBackend(t, "http://b2:80", 1)
	b3 := makeBackend(t, "http://b3:80", 1)

	rr := strategy.NewRoundRobin([]*strategy.Backend{b1, b2, b3})
	counts := countDistribution(t, rr, 99)

	assert.Equal(t, 33, counts["http://b1:80"], "b1 should receive 1/3 of requests")
	assert.Equal(t, 33, counts["http://b2:80"], "b2 should receive 1/3 of requests")
	assert.Equal(t, 33, counts["http://b3:80"], "b3 should receive 1/3 of requests")
}

func TestRoundRobin_SkipsUnhealthy(t *testing.T) {
	b1 := makeBackend(t, "http://b1:80", 1)
	b2 := makeBackend(t, "http://b2:80", 1)
	b3 := makeBackend(t, "http://b3:80", 1)
	b2.SetHealthy(false) // mark b2 unhealthy

	rr := strategy.NewRoundRobin([]*strategy.Backend{b1, b2, b3})
	counts := countDistribution(t, rr, 100)

	assert.Equal(t, 0, counts["http://b2:80"], "unhealthy backend must receive no traffic")
	assert.Greater(t, counts["http://b1:80"], 0, "b1 must receive some traffic")
	assert.Greater(t, counts["http://b3:80"], 0, "b3 must receive some traffic")
}

func TestRoundRobin_AllUnhealthy_ReturnsError(t *testing.T) {
	b1 := makeBackend(t, "http://b1:80", 1)
	b1.SetHealthy(false)

	rr := strategy.NewRoundRobin([]*strategy.Backend{b1})
	_, err := rr.Next()

	assert.True(t, errors.Is(err, strategy.ErrNoHealthyBackend))
}

// ── WeightedRoundRobin ───────────────────────────────────────────────────────

func TestWeightedRR_ProportionalDistribution(t *testing.T) {
	b1 := makeBackend(t, "http://b1:80", 1) // should get ~100 / 300
	b2 := makeBackend(t, "http://b2:80", 2) // should get ~200 / 300

	wrr := strategy.NewWeightedRoundRobin([]*strategy.Backend{b1, b2})
	counts := countDistribution(t, wrr, 300)

	// Allow ±5% tolerance — smooth WRR is deterministic, so we use tight bounds.
	assert.InDelta(t, 100, counts["http://b1:80"], 5, "b1 weight=1 should get ~1/3")
	assert.InDelta(t, 200, counts["http://b2:80"], 5, "b2 weight=2 should get ~2/3")
}

func TestWeightedRR_SkipsUnhealthy(t *testing.T) {
	b1 := makeBackend(t, "http://b1:80", 1)
	b2 := makeBackend(t, "http://b2:80", 10)
	b2.SetHealthy(false)

	wrr := strategy.NewWeightedRoundRobin([]*strategy.Backend{b1, b2})
	counts := countDistribution(t, wrr, 20)

	assert.Equal(t, 0, counts["http://b2:80"], "unhealthy backend must receive no traffic")
	assert.Equal(t, 20, counts["http://b1:80"])
}

func TestWeightedRR_AllUnhealthy_ReturnsError(t *testing.T) {
	b1 := makeBackend(t, "http://b1:80", 1)
	b1.SetHealthy(false)

	wrr := strategy.NewWeightedRoundRobin([]*strategy.Backend{b1})
	_, err := wrr.Next()

	assert.True(t, errors.Is(err, strategy.ErrNoHealthyBackend))
}

// ── LeastConnections ─────────────────────────────────────────────────────────

func TestLeastConnections_PicksLowest(t *testing.T) {
	b1 := makeBackend(t, "http://b1:80", 1)
	b2 := makeBackend(t, "http://b2:80", 1)

	// Manually set b1 to have 5 active connections.
	for i := 0; i < 5; i++ {
		b1.IncConns()
	}

	lc := strategy.NewLeastConnections([]*strategy.Backend{b1, b2})
	got, err := lc.Next()
	require.NoError(t, err)

	assert.Equal(t, "http://b2:80", got.RawURL, "b2 has fewer conns and should be selected")
}

func TestLeastConnections_AllUnhealthy_ReturnsError(t *testing.T) {
	b1 := makeBackend(t, "http://b1:80", 1)
	b1.SetHealthy(false)

	lc := strategy.NewLeastConnections([]*strategy.Backend{b1})
	_, err := lc.Next()

	assert.True(t, errors.Is(err, strategy.ErrNoHealthyBackend))
}

func TestLeastConnections_Done_DecrementsCounter(t *testing.T) {
	b := makeBackend(t, "http://b1:80", 1)
	lc := strategy.NewLeastConnections([]*strategy.Backend{b})

	picked, err := lc.Next()
	require.NoError(t, err)
	assert.Equal(t, int64(1), picked.ActiveConns(), "Next() should increment counter")

	lc.Done(picked)
	assert.Equal(t, int64(0), picked.ActiveConns(), "Done() should decrement counter")
}

// ── Factory ───────────────────────────────────────────────────────────────────

func TestPickerFactory_ValidStrategies(t *testing.T) {
	backends := []*strategy.Backend{makeBackend(t, "http://b1:80", 1)}

	for _, name := range []string{"round_robin", "", "weighted_round_robin", "least_connections"} {
		p, err := strategy.New(name, backends)
		assert.NoError(t, err, "strategy %q should be valid", name)
		assert.NotNil(t, p)
	}
}

func TestPickerFactory_UnknownStrategy_ReturnsError(t *testing.T) {
	backends := []*strategy.Backend{makeBackend(t, "http://b1:80", 1)}

	_, err := strategy.New("magic_balancer", backends)
	assert.Error(t, err)
}

func TestPickerFactory_EmptyBackends_ReturnsError(t *testing.T) {
	_, err := strategy.New("round_robin", nil)
	assert.Error(t, err)
}
