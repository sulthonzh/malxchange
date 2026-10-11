package events

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestEnvelopeRoundTrip(t *testing.T) {
	type fill struct {
		Market   string `json:"market"`
		Price    string `json:"price"` // decimal strings on the wire (ADR-0005)
		Amount   string `json:"amount"`
		TakerFee string `json:"takerFee"`
	}
	in := fill{Market: "BTC/USDT", Price: "65000.5", Amount: "0.001", TakerFee: "0.000001"}

	id := uuid.NewString()
	_, body, err := New(id, "BTC/USDT", TopicTradeFill, time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC), in)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	e, err := Decode(body)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if e.ID != id || e.Key != "BTC/USDT" || e.Type != TopicTradeFill {
		t.Errorf("decoded wrong header: %+v", e)
	}
	if got := string(e.Data); got != `{"market":"BTC/USDT","price":"65000.5","amount":"0.001","takerFee":"0.000001"}` {
		t.Errorf("data = %s", got)
	}
}

func TestDecodeRejectsIncomplete(t *testing.T) {
	if _, err := Decode([]byte(`{"id":"x","key":"k"}`)); err == nil {
		t.Error("missing type must fail")
	}
	if _, err := Decode([]byte(`not json`)); err == nil {
		t.Error("garbage must fail")
	}
}
