package events

// Ticker is the rolling per-Market summary published on TopicTicker
// (ticket #32; ADR-0011: the public `ticker` channel relays this payload
// as-is, and the internal REST endpoints serve the same shape). Money
// values are decimal strings (ADR-0005).
//
// A window with no Fills publishes honestly empty: Last/High/Low are ""
// (a range over an empty window is undefined) and Turnover is "0" (the
// empty sum), so the front never waits for a first tick.
type Ticker struct {
	Market   string `json:"market"`
	Last     string `json:"last"`     // newest in-window Fill price; "" when the window holds no Fills
	High     string `json:"high"`     // 24h high; "" when the window holds no Fills
	Low      string `json:"low"`      // 24h low; "" when the window holds no Fills
	Turnover string `json:"turnover"` // 24h quote-side Turnover; "0" when the window holds no Fills
}
