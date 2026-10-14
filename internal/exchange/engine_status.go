// Engine status wire (ticket #61): the view every admin Role sees.
// Extends the ADR-0010 status RPC with the one thing it omitted — the
// command consumer's lag (committed vs end offset on order.command) —
// alongside the per-Market Book Ready state, open Orders, Halted flag,
// and sequence the engine already tracks.
package exchange

import (
	"context"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/syaikhipin/malxchange/internal/events"
)

// EngineStatus is the /engine/status body. ConsumerLag is nil when the
// broker cannot answer (the status page shows "unknown" rather than a
// lying zero).
type EngineStatus struct {
	OK          bool             `json:"ok"`
	Markets     []StatusSnapshot `json:"markets"`
	ConsumerLag *int64           `json:"consumerLag"`
	At          time.Time        `json:"at"`
}

// StatusWithLag snapshots the books and, in the same call, asks the
// broker for the consumer group's lag. The lag probe is bounded by the
// client's request timeout — a stalled broker delays the page once,
// never wedges the engine.
func StatusWithLag(ctx context.Context, e *Engine, cl *kgo.Client) EngineStatus {
	lag, ok := consumerLag(ctx, cl, ConsumerGroup, events.TopicOrderCommand)
	var lagPtr *int64
	if ok {
		lagPtr = &lag
	}
	return EngineStatus{
		OK:          true,
		Markets:     e.Status(),
		ConsumerLag: lagPtr,
		At:          time.Now().UTC(),
	}
}

// consumerLag sums the group's committed-vs-end-offset distance on the
// topic across partitions. False means "could not be computed": the
// group is unknown to the broker or any offset fetch failed (kadm
// marks unreadable partitions with Lag = -1).
func consumerLag(ctx context.Context, cl *kgo.Client, group, topic string) (int64, bool) {
	admin := kadm.NewClient(cl)
	lags, err := admin.Lag(ctx, group)
	if err != nil {
		return 0, false
	}
	return sumLag(lags, group, topic)
}

// sumLag is the pure arithmetic over kadm's answer: total committed-vs-
// end distance for one group on one topic. Unknown group, a describe or
// fetch error, or any unreadable partition means unknown.
func sumLag(lags kadm.DescribedGroupLags, group, topic string) (int64, bool) {
	described, ok := lags[group]
	if !ok || described.DescribeErr != nil || described.FetchErr != nil {
		return 0, false
	}
	var total int64
	known := false
	for _, partitions := range described.Lag {
		for _, m := range partitions {
			if m.Topic != topic {
				continue
			}
			if m.Err != nil || m.Lag < 0 {
				return 0, false
			}
			total += m.Lag
			known = true
		}
	}
	return total, known
}
