// Package events defines the topic contracts and the wire envelope shared
// by all services (ADR-0002: topics per bounded context; ADR-0005: money
// values travel as decimal strings inside Data). Topics are created by the
// service that PRODUCES them, at boot, via kafka.EnsureTopics — auto-create
// stays off so a typo cannot spawn a phantom topic.
package events

import (
	"encoding/json"
	"fmt"
	"time"
)

// Topic names. One topic per bounded-context fact; keys (below) provide
// per-entity ordering within a topic.
const (
	// TopicOrderCommand carries member order placements/cancels from api
	// to the engine (consumed by exchange; ADR-0010 fold input).
	TopicOrderCommand = "order.command"
	// TopicTradeFill carries self-contained fill events produced by
	// exchange (ADR-0009): both sides, amounts, fees, turnover, ids.
	TopicTradeFill = "trade.fill"
	// TopicOrderCompleted carries order terminal states from exchange.
	TopicOrderCompleted = "order.completed"
	// TopicCancelRejected answers cancels naming unknown orders.
	TopicCancelRejected = "cancel.rejected"
	// TopicMarketPlate carries the once-per-batch full Depth per Market
	// (ADR-0010); market persists the last-known plate, api relays it.
	TopicMarketPlate = "market.plate"
	// TopicMarketUpdated carries the self-contained full Market row
	// (ticket #29): the lawful source for market's Market copy and api's
	// listings, emitted at exchange boot and on every accepted config
	// change. Retention matches trade.fill so a rebuild replay converges.
	TopicMarketUpdated = "market.updated"
	// TopicTicker carries the per-Market rolling summary (≤1s cadence,
	// published by market; ADR-0011).
	TopicTicker = "ticker"
	// TopicMemberRegister is emitted by api so wallet opens Ledger
	// accounts for the new Member (ADR-0006).
	TopicMemberRegister = "member.register"
)

// Envelope is the single wire format on every topic. Data is opaque here;
// producing/consuming services own its schema. Money inside Data is always
// decimal strings.
type Envelope struct {
	ID         string          `json:"id"`         // outbox UUID; consumers dedupe on (key, id)
	Type       string          `json:"type"`       // topic name; redundant for debugging
	Key        string          `json:"key"`        // business key; Kafka record key
	OccurredAt time.Time       `json:"occurredAt"` // producer wall clock, RFC3339 in JSON
	Data       json.RawMessage `json:"data"`
}

// New builds an Envelope with a fresh UUID and marshals it to the bytes
// that go into the outbox row.
func New(id, key string, typ string, occurredAt time.Time, data any) (Envelope, []byte, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return Envelope{}, nil, fmt.Errorf("events: marshal data: %w", err)
	}
	e := Envelope{ID: id, Type: typ, Key: key, OccurredAt: occurredAt, Data: raw}
	b, err := json.Marshal(e)
	if err != nil {
		return Envelope{}, nil, fmt.Errorf("events: marshal envelope: %w", err)
	}
	return e, b, nil
}

// Decode parses envelope bytes off a topic.
func Decode(b []byte) (Envelope, error) {
	var e Envelope
	if err := json.Unmarshal(b, &e); err != nil {
		return Envelope{}, fmt.Errorf("events: decode: %w", err)
	}
	if e.ID == "" || e.Key == "" || e.Type == "" {
		return Envelope{}, fmt.Errorf("events: envelope missing id/key/type")
	}
	return e, nil
}
