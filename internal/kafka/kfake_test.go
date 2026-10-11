package kafka

import (
	"context"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/syaikhipin/malxchange/internal/config"
)

func TestKfakeGroupRoundtrip(t *testing.T) {
	broker, err := kfake.NewCluster(kfake.NumBrokers(1))
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	addrs := broker.ListenAddrs()
	if err := broker.CreateTopic("probe.t", 1, nil); err != nil {
		t.Fatal(err)
	}
	cfg := config.Kafka{Seeds: addrs}

	prod, err := kgo.NewClient(kgo.SeedBrokers(addrs...))
	if err != nil {
		t.Fatal(err)
	}
	defer prod.Close()
	if err := prod.ProduceSync(context.Background(), &kgo.Record{Topic: "probe.t", Key: []byte("k"), Value: []byte("x")}).FirstErr(); err != nil {
		t.Fatal(err)
	}

	cons, err := NewConsumer(cfg, "probe-group", []string{"probe.t"}, kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	defer cons.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	fetches := cons.PollRecords(ctx, 10)
	for rec := range fetches.RecordsAll() {
		if rec.Topic == "probe.t" {
			return // got it
		}
	}
	_ = time.Second
	t.Fatalf("no records delivered from kfake group consumer; fetch err: %v", fetches.Err0())
}

func TestKfakeFullProducerConsumer(t *testing.T) {
	broker, err := kfake.NewCluster(kfake.NumBrokers(1))
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	if err := broker.CreateTopic("probe.t", 1, nil); err != nil {
		t.Fatal(err)
	}
	cfg := config.Kafka{Seeds: broker.ListenAddrs()}
	prod, err := NewProducer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer prod.Close()
	if err := (Publisher{Client: prod}).Publish(context.Background(), "probe.t", "k", []byte("x")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	cons, err := NewConsumer(cfg, "full-probe", []string{"probe.t"}, kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	defer cons.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	fetches := cons.PollRecords(ctx, 10)
	for rec := range fetches.RecordsAll() {
		if rec.Topic == "probe.t" {
			return
		}
	}
	t.Fatalf("no records; err=%v", fetches.Err0())
}
