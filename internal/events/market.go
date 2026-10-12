package events

// MarketUpdated is the self-contained full markets-table row on
// TopicMarketUpdated (ticket #29). It is the lawful source for market's
// own Market copy and api's listings, so no consumer needs a cross-schema
// read. Exchange emits one per known Market at boot (after Book recovery)
// and one per accepted config-watch change, keyed by Market symbol.
//
// Money values are decimal strings (ADR-0005); "" means unbounded for the
// collar and limit fields. The optional caps are absent when unset.
type MarketUpdated struct {
	Market     string `json:"market"`
	BaseCoin   string `json:"baseCoin"`
	QuoteCoin  string `json:"quoteCoin"`
	BaseScale  int    `json:"baseScale"`
	QuoteScale int    `json:"quoteScale"`
	TickSize   string `json:"tickSize"` // price quantum, decimal string
	LotSize    string `json:"lotSize"`  // amount quantum, decimal string

	MakerFeeRate string `json:"makerFeeRate"`
	TakerFeeRate string `json:"takerFeeRate"`
	MinPrice     string `json:"minPrice"` // "" = unbounded
	MaxPrice     string `json:"maxPrice"`
	MinAmount    string `json:"minAmount"`
	MaxAmount    string `json:"maxAmount"`
	MinTurnover  string `json:"minTurnover"`

	MarketOrdersEnabled bool `json:"marketOrdersEnabled"`
	Enabled             bool `json:"enabled"` // off = Halted; cancels still flow
	Visible             bool `json:"visible"` // listed on the front

	Sort     int    `json:"sort"`
	Zone     string `json:"zone"`
	Featured bool   `json:"featured"`

	MaxOpenOrders *int32 `json:"maxOpenOrders,omitempty"` // per-Member cap
	TTLSeconds    *int32 `json:"ttlSeconds,omitempty"`    // resting-order TTL; absent = none
}
