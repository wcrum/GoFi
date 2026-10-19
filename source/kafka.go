//go:build !gofi_nokafka

package source

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/observability"
	"github.com/wcrum/gofi/spec"
)

// kafkaPollRecords bounds one poll; offsets are marked record by record.
const kafkaPollRecords = 500

// kafkaOptions builds the franz-go client options for a consumer group.
func kafkaOptions(flow string, cfg *spec.KafkaSubscribe) ([]kgo.Opt, error) {
	group := cfg.Group
	if group == "" {
		group = "gofi-" + flow
	}
	clientID := cfg.ClientID
	if clientID == "" {
		clientID = "gofi-" + flow
	}
	start := kgo.NewOffset().AtEnd()
	if cfg.StartOffset == "earliest" {
		start = kgo.NewOffset().AtStart()
	}
	brokers := make([]string, len(cfg.Brokers))
	for i, broker := range cfg.Brokers {
		brokers[i] = strings.TrimSpace(broker)
	}
	opts := []kgo.Opt{
		kgo.SeedBrokers(brokers...),
		kgo.ClientID(clientID),
		kgo.ConsumerGroup(group),
		kgo.ConsumeTopics(cfg.Topics...),
		kgo.ConsumeResetOffset(start),
		// Commit only records whose pipeline run returned; commit before a
		// rebalance hands partitions elsewhere (the default revoke handler
		// commits marks), and never mid-poll.
		kgo.AutoCommitMarks(),
		kgo.BlockRebalanceOnPoll(),
		kgo.FetchMaxPartitionBytes(MaxMessageBytes),
		kgo.FetchMaxBytes(4 * MaxMessageBytes),
	}
	if tlsConfig, err := cfg.TLS.Config(false); err != nil {
		return nil, fmt.Errorf("kafka %w", err)
	} else if tlsConfig != nil {
		opts = append(opts, kgo.DialTLSConfig(tlsConfig))
	}
	if mechanism := kafkaSASL(cfg.SASL); mechanism != nil {
		opts = append(opts, kgo.SASL(mechanism))
	}
	return opts, nil
}

func kafkaSASL(auth *spec.KafkaSASL) sasl.Mechanism {
	if auth == nil {
		return nil
	}
	switch strings.ToUpper(auth.Mechanism) {
	case "PLAIN":
		return plain.Auth{User: auth.Username, Pass: auth.Password}.AsMechanism()
	case "SCRAM-SHA-256":
		return scram.Auth{User: auth.Username, Pass: auth.Password}.AsSha256Mechanism()
	case "SCRAM-SHA-512":
		return scram.Auth{User: auth.Username, Pass: auth.Password}.AsSha512Mechanism()
	}
	return nil
}

// runKafka consumes in a consumer group until ctx ends or a poll fails. Each
// record is one emit; its offset is marked after emit returns (the pipeline
// run, sinks included) and committed by the client's autocommit, on
// rebalance, and on shutdown. A crash between a pipeline run and the next
// commit redelivers those records: at-least-once into the pipeline.
func runKafka(ctx context.Context, flow string, cfg *spec.KafkaSubscribe, emit Emit) error {
	opts, err := kafkaOptions(flow, cfg)
	if err != nil {
		return err
	}
	client, err := kgo.NewClient(opts...)
	if err != nil {
		return err
	}
	defer func() {
		commitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.CommitMarkedOffsets(commitCtx); err != nil {
			slog.Warn("kafka: final offset commit failed", "flow", flow, "error", err)
		}
		client.CloseAllowingRebalance()
	}()
	pingCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	err = client.Ping(pingCtx)
	cancel()
	if err != nil {
		return fmt.Errorf("reach brokers %v: %w", cfg.Brokers, err)
	}
	for {
		fetches := client.PollRecords(ctx, kafkaPollRecords)
		if ctx.Err() != nil || fetches.IsClientClosed() {
			return nil
		}
		var failure error
		fetches.EachError(func(topic string, partition int32, err error) {
			if errors.Is(err, context.Canceled) {
				return
			}
			observability.SourceEvent(flow, "kafka", "error")
			failure = fmt.Errorf("fetch %s[%d]: %w", topic, partition, err)
		})
		fetches.EachRecord(func(record *kgo.Record) {
			if ctx.Err() != nil {
				return // unmarked: redelivered to the next owner
			}
			if len(record.Value) > MaxMessageBytes {
				observability.SourceEvent(flow, "kafka", "error")
				slog.Warn("kafka: record over the size cap skipped", "flow", flow, "topic", record.Topic, "partition", record.Partition, "offset", record.Offset)
			} else {
				observability.SourceEvent(flow, "kafka", "ok")
				emit(message.WithSourceMeta(ctx, kafkaMeta(record)), record.Value, "kafka:"+record.Topic)
			}
			client.MarkCommitRecords(record)
		})
		client.AllowRebalance()
		if failure != nil && fetches.NumRecords() == 0 {
			return failure
		}
		if failure != nil {
			slog.Warn("kafka: fetch error", "flow", flow, "error", failure)
		}
	}
}

// kafkaMeta is a record's metadata for meta["kafka.*"].
func kafkaMeta(record *kgo.Record) map[string]string {
	meta := map[string]string{
		"kafka.topic":     record.Topic,
		"kafka.partition": strconv.FormatInt(int64(record.Partition), 10),
		"kafka.offset":    strconv.FormatInt(record.Offset, 10),
	}
	if record.Key != nil {
		meta["kafka.key"] = string(record.Key)
	}
	if !record.Timestamp.IsZero() {
		meta["kafka.timestamp"] = record.Timestamp.UTC().Format(time.RFC3339Nano)
	}
	for _, header := range record.Headers {
		meta["kafka.header."+header.Key] = string(header.Value)
	}
	return meta
}
