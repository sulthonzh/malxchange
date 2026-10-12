package events

import (
	"testing"
	"time"
)

// TestMarketUpdatedTopicContract pins the shared topic contract (ticket
// #29): exchange owns market.updated, created at boot with no auto-create,
// one partition (per-Market ordering via the envelope key), and trade.fill
// retention so a rebuild replay converges.
func TestMarketUpdatedTopicContract(t *testing.T) {
	owned := OwnedTopics("exchange")
	for _, td := range owned {
		if td.Topic != TopicMarketUpdated {
			continue
		}
		if td.Partitions != 1 {
			t.Errorf("partitions = %d, want 1", td.Partitions)
		}
		if td.Retention != 7*24*time.Hour {
			t.Errorf("retention = %s, want 168h (matches trade.fill)", td.Retention)
		}
		return
	}
	t.Fatalf("exchange does not own %q: %+v", TopicMarketUpdated, owned)
}

// TestMarketUpdatedPayloadDecimalStrings pins the full-row wire shape: the
// complete markets row with every money value a decimal string (ADR-0005),
// "" meaning unbounded, and absent optional caps omitted.
func TestMarketUpdatedPayloadDecimalStrings(t *testing.T) {
	maxOpen := int32(10)
	ttl := int32(300)
	raw := Raw(MarketUpdated{
		Market: "BTC/USDT", BaseCoin: "BTC", QuoteCoin: "USDT",
		BaseScale: 8, QuoteScale: 2,
		TickSize: "0.1", LotSize: "0.0001",
		MakerFeeRate: "0.001", TakerFeeRate: "0.0015",
		MinPrice: "", MaxPrice: "1000000", MinAmount: "0", MaxAmount: "", MinTurnover: "5",
		MarketOrdersEnabled: true,
		Enabled:             true,
		Visible:             true,
		Sort:                1, Zone: "main", Featured: true,
		MaxOpenOrders: &maxOpen, TTLSeconds: &ttl,
	})
	want := `{"market":"BTC/USDT","baseCoin":"BTC","quoteCoin":"USDT","baseScale":8,"quoteScale":2,` +
		`"tickSize":"0.1","lotSize":"0.0001","makerFeeRate":"0.001","takerFeeRate":"0.0015",` +
		`"minPrice":"","maxPrice":"1000000","minAmount":"0","maxAmount":"","minTurnover":"5",` +
		`"marketOrdersEnabled":true,"enabled":true,"visible":true,` +
		`"sort":1,"zone":"main","featured":true,"maxOpenOrders":10,"ttlSeconds":300}`
	if string(raw) != want {
		t.Errorf("payload = %s\nwant    %s", raw, want)
	}

	raw = Raw(MarketUpdated{Market: "ETH/USDT"})
	want = `{"market":"ETH/USDT","baseCoin":"","quoteCoin":"","baseScale":0,"quoteScale":0,` +
		`"tickSize":"","lotSize":"","makerFeeRate":"","takerFeeRate":"",` +
		`"minPrice":"","maxPrice":"","minAmount":"","maxAmount":"","minTurnover":"",` +
		`"marketOrdersEnabled":false,"enabled":false,"visible":false,` +
		`"sort":0,"zone":"","featured":false}`
	if string(raw) != want {
		t.Errorf("nil-cap payload = %s\nwant            %s", raw, want)
	}
}
