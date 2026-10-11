//go:build integration

package exchange

import (
	"context"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/syaikhipin/malxchange/internal/config"
	"github.com/syaikhipin/malxchange/internal/events"
	"github.com/syaikhipin/malxchange/internal/kafka"
)

func TestExchangeKfakeProbe(t *testing.T) {
	broker, err := kfake.NewCluster(kfake.NumBrokers(1))
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	if err := broker.CreateTopic(events.TopicOrderCommand, 1, nil); err != nil {
		t.Fatal(err)
	}
	cfg := config.Kafka{Seeds: broker.ListenAddrs()}

	prod, err := kafka.NewProducer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer prod.Close()

	_, body, err := events.New("probe-1", "BTC/USDT", events.TopicOrderCommand, time.Now().UTC(),
		placeCmd("p1", "m", "sell", "limit", "50000.10", "0.0001"))
	if err != nil {
		t.Fatal(err)
	}
	if err := prod.ProduceSync(context.Background(), &kgo.Record{
		Topic: events.TopicOrderCommand, Key: []byte("BTC/USDT"), Value: body,
	}).FirstErr(); err != nil {
		t.Fatalf("produce: %v", err)
	}

	cons, err := kafka.NewConsumer(cfg, "exprobe", []string{events.TopicOrderCommand},
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	defer cons.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	fetches := cons.PollRecords(ctx, 10)
	n := 0
	for range fetches.RecordsAll() {
		n++
	}
	if n == 0 {
		t.Fatalf("no records; err=%v", fetches.Err0())
	}
}
