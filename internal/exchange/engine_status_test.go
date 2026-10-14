// The lag arithmetic behind /engine/status (ticket #61): pure summing
// over kadm's describe+fetch answer — the served wire (null before the
// group exists, a number once the broker answers) is the integration
// test's.
package exchange

import (
	"errors"
	"testing"

	"github.com/twmb/franz-go/pkg/kadm"
)

func lagPartition(topic string, partition int32, commitAt, end, lag int64, err error) kadm.GroupMemberLag {
	return kadm.GroupMemberLag{
		Topic: topic, Partition: partition,
		Commit: kadm.Offset{At: commitAt},
		End:    kadm.ListedOffset{Offset: end},
		Lag:    lag, Err: err,
	}
}

func TestSumLagSumsTopicPartitions(t *testing.T) {
	lags := kadm.DescribedGroupLags{
		"exchange-engine": {
			DescribeErr: nil, FetchErr: nil,
			Lag: kadm.GroupLag{
				"order.command": {
					0: lagPartition("order.command", 0, 5, 8, 3, nil),
					1: lagPartition("order.command", 1, 2, 4, 2, nil),
				},
				"trade.fill": {
					0: lagPartition("trade.fill", 0, 0, 99, 99, nil),
				},
			},
		},
	}
	got, ok := sumLag(lags, "exchange-engine", "order.command")
	if !ok || got != 5 {
		t.Fatalf("sumLag = %d ok=%v, want 5 true (only order.command summed)", got, ok)
	}
}

func TestSumLagUnknownGroup(t *testing.T) {
	if _, ok := sumLag(kadm.DescribedGroupLags{}, "exchange-engine", "order.command"); ok {
		t.Fatal("missing group must be unknown")
	}
	lags := kadm.DescribedGroupLags{
		"exchange-engine": {DescribeErr: errors.New("coordinator down")},
	}
	if _, ok := sumLag(lags, "exchange-engine", "order.command"); ok {
		t.Fatal("describe error must be unknown")
	}
	lags = kadm.DescribedGroupLags{
		"exchange-engine": {FetchErr: errors.New("offsets fetch failed")},
	}
	if _, ok := sumLag(lags, "exchange-engine", "order.command"); ok {
		t.Fatal("fetch error must be unknown")
	}
}

func TestSumLagUnreadablePartition(t *testing.T) {
	// kadm marks a partition whose offsets could not load with
	// Lag = -1: one bad partition makes the whole number a lie.
	lags := kadm.DescribedGroupLags{
		"exchange-engine": {
			Lag: kadm.GroupLag{
				"order.command": {
					0: lagPartition("order.command", 0, 5, 8, 3, nil),
					1: lagPartition("order.command", 1, -1, -1, -1, errors.New("load failed")),
				},
			},
		},
	}
	if _, ok := sumLag(lags, "exchange-engine", "order.command"); ok {
		t.Fatal("a -1 partition must make the total unknown")
	}
}

func TestSumLagZeroLagIsKnown(t *testing.T) {
	// A fully caught-up group is a REAL zero, not unknown.
	lags := kadm.DescribedGroupLags{
		"exchange-engine": {
			Lag: kadm.GroupLag{
				"order.command": {
					0: lagPartition("order.command", 0, 8, 8, 0, nil),
				},
			},
		},
	}
	got, ok := sumLag(lags, "exchange-engine", "order.command")
	if !ok || got != 0 {
		t.Fatalf("sumLag = %d ok=%v, want 0 true", got, ok)
	}
}
