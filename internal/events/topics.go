package events

import "time"

// TopicDef pairs a topic with its broker settings; the producing service
// creates it at boot (see kafka.EnsureTopics).

// ProducedBy lists the topics a service owns (creates + produces to).
// Context map (wayfinder #4): api is the member surface, exchange the
// engine, market the projector, wallet the Ledger, admin the back office.
var producedBy = map[string][]struct {
	Topic      string
	Partitions int32
	Retention  time.Duration
}{
	"api": {
		{TopicOrderCommand, 1, 7 * 24 * time.Hour},
		{TopicMemberRegister, 1, 7 * 24 * time.Hour},
	},
	"exchange": {
		{TopicTradeFill, 1, 7 * 24 * time.Hour}, // settlement history
		{TopicOrderCompleted, 1, 7 * 24 * time.Hour},
		{TopicCancelRejected, 1, 24 * time.Hour},
		{TopicMarketPlate, 1, time.Hour},            // transient; market persists last-known
		{TopicMarketUpdated, 1, 7 * 24 * time.Hour}, // config history; replay converges
	},
	"market": {
		{TopicTicker, 1, time.Hour},
	},
	// wallet and admin produce no topics in v1.
}

// OwnedTopics returns the topic definitions the named service must create
// at boot, or nil if it produces nothing.
func OwnedTopics(service string) []struct {
	Topic      string
	Partitions int32
	Retention  time.Duration
} {
	return producedBy[service]
}
