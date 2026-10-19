package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/wcrum/gofi/message"
	"github.com/wcrum/gofi/observability"
	"github.com/wcrum/gofi/spec"
)

// Redis stream consumer tuning.
var redisStream = struct {
	count      int64
	block      time.Duration
	claimIdle  time.Duration // pending entries idle this long are taken over
	claimEvery time.Duration
}{100, 2 * time.Second, 5 * time.Minute, time.Minute}

// redisOptions parses the URL and applies explicit credentials and TLS.
func redisOptions(flow string, cfg *spec.RedisSubscribe) (*redis.Options, error) {
	opts, err := redis.ParseURL(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("redis url: %v", errors.Unwrap(err))
	}
	if cfg.Username != "" || cfg.Password != "" {
		opts.Username, opts.Password = cfg.Username, cfg.Password
	}
	if opts.TLSConfig != nil {
		tlsConfig, err := cfg.TLS.Config(true)
		if err != nil {
			return nil, fmt.Errorf("redis %w", err)
		}
		tlsConfig.ServerName = opts.TLSConfig.ServerName
		opts.TLSConfig = tlsConfig
	}
	opts.ClientName = "gofi-" + flow
	return opts, nil
}

// runRedis reads a stream with a consumer group or holds a Pub/Sub
// subscription until ctx ends or the connection fails.
func runRedis(ctx context.Context, flow string, cfg *spec.RedisSubscribe, emit Emit) error {
	opts, err := redisOptions(flow, cfg)
	if err != nil {
		return err
	}
	client := redis.NewClient(opts)
	defer client.Close()
	if err := client.Ping(ctx).Err(); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("connect %s: %w", redactURL(cfg.URL), err)
	}
	if cfg.Stream != "" {
		return (&redisStreamReader{client: client, flow: flow, cfg: cfg, emit: emit}).run(ctx)
	}
	return runRedisPubSub(ctx, client, flow, cfg, emit)
}

// runRedisPubSub forwards each published message as one emit. Pub/Sub has
// no acknowledgement or replay: messages published while disconnected are
// lost (at-most-once). The client resubscribes after reconnects.
func runRedisPubSub(ctx context.Context, client *redis.Client, flow string, cfg *spec.RedisSubscribe, emit Emit) error {
	var ps *redis.PubSub
	if cfg.Pattern {
		ps = client.PSubscribe(ctx, cfg.Channels...)
	} else {
		ps = client.Subscribe(ctx, cfg.Channels...)
	}
	defer ps.Close()
	if _, err := ps.Receive(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("subscribe %v: %w", cfg.Channels, err)
	}
	messages := ps.Channel(redis.WithChannelSize(1000))
	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-messages:
			if !ok {
				return fmt.Errorf("redis subscription closed")
			}
			meta := map[string]string{"redis.channel": msg.Channel}
			if msg.Pattern != "" {
				meta["redis.pattern"] = msg.Pattern
			}
			observability.SourceEvent(flow, "redis", "ok")
			emit(message.WithSourceMeta(ctx, meta), []byte(msg.Payload), "redis:"+msg.Channel)
		}
	}
}

// redisStreamReader consumes one stream in a consumer group. Each entry is
// one emit and is XACKed after emit returns (the pipeline run, sinks
// included). Entries delivered but never acked (a crash mid-run) stay
// pending: this consumer re-reads its own pending entries on connect, and
// takes over other consumers' entries idle for claimIdle (replicas with
// changed hostnames). At-least-once into the pipeline.
type redisStreamReader struct {
	client *redis.Client
	flow   string
	cfg    *spec.RedisSubscribe
	emit   Emit
	group  string
	name   string
}

func (r *redisStreamReader) run(ctx context.Context) error {
	r.group = r.cfg.Group
	if r.group == "" {
		r.group = "gofi-" + r.flow
	}
	r.name = r.cfg.Consumer
	if r.name == "" {
		host, err := os.Hostname()
		if err != nil || host == "" {
			host = "gofi-" + r.flow
		}
		r.name = host
	}
	start := "$"
	if r.cfg.Start == "earliest" {
		start = "0"
	}
	if err := r.client.XGroupCreateMkStream(ctx, r.cfg.Stream, r.group, start).Err(); err != nil && !strings.HasPrefix(err.Error(), "BUSYGROUP") {
		return fmt.Errorf("create group %q on stream %q: %w", r.group, r.cfg.Stream, err)
	}
	// Own pending entries first: delivered to this consumer before a restart
	// or disconnect but never acknowledged.
	for {
		n, err := r.read(ctx, "0", 0)
		if err != nil || ctx.Err() != nil {
			return err
		}
		if n == 0 {
			break
		}
	}
	lastClaim := time.Time{}
	for ctx.Err() == nil {
		if time.Since(lastClaim) >= redisStream.claimEvery {
			if err := r.claim(ctx); err != nil {
				return err
			}
			lastClaim = time.Now()
		}
		if _, err := r.read(ctx, ">", redisStream.block); err != nil {
			return err
		}
	}
	return nil
}

// read reads from id ("0": own pending, ">": new) and handles each entry;
// it returns how many entries it read.
func (r *redisStreamReader) read(ctx context.Context, id string, block time.Duration) (int, error) {
	args := &redis.XReadGroupArgs{Group: r.group, Consumer: r.name, Streams: []string{r.cfg.Stream, id}, Count: redisStream.count, Block: block}
	if block == 0 {
		args.Block = -1 // no BLOCK argument for pending reads
	}
	streams, err := r.client.XReadGroup(ctx, args).Result()
	if errors.Is(err, redis.Nil) || ctx.Err() != nil {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("XREADGROUP %q: %w", r.cfg.Stream, err)
	}
	n := 0
	for _, stream := range streams {
		for _, entry := range stream.Messages {
			n++
			if err := r.handle(ctx, entry); err != nil || ctx.Err() != nil {
				return n, err
			}
		}
	}
	return n, nil
}

// claim takes over entries other consumers left pending for claimIdle.
func (r *redisStreamReader) claim(ctx context.Context) error {
	next := "0-0"
	for {
		entries, cursor, err := r.client.XAutoClaim(ctx, &redis.XAutoClaimArgs{Stream: r.cfg.Stream, Group: r.group, Consumer: r.name, MinIdle: redisStream.claimIdle, Start: next, Count: redisStream.count}).Result()
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			return fmt.Errorf("XAUTOCLAIM %q: %w", r.cfg.Stream, err)
		}
		for _, entry := range entries {
			if err := r.handle(ctx, entry); err != nil || ctx.Err() != nil {
				return err
			}
		}
		if cursor == "0-0" || cursor == "" {
			return nil
		}
		next = cursor
	}
}

func (r *redisStreamReader) handle(ctx context.Context, entry redis.XMessage) error {
	if entry.Values == nil {
		// Pending entry that was deleted from the stream: nothing to run.
		return r.ack(ctx, entry.ID)
	}
	data, err := redisEntryData(entry, r.cfg.Field)
	if err != nil {
		observability.SourceEvent(r.flow, "redis", "error")
		slog.Warn("redis: stream entry skipped", "flow", r.flow, "stream", r.cfg.Stream, "id", entry.ID, "error", err)
	} else {
		observability.SourceEvent(r.flow, "redis", "ok")
		meta := map[string]string{"redis.stream": r.cfg.Stream, "redis.id": entry.ID}
		r.emit(message.WithSourceMeta(ctx, meta), data, "redis:"+r.cfg.Stream)
	}
	if ctx.Err() != nil {
		return nil // unacked: re-read from pending on the next connect
	}
	return r.ack(ctx, entry.ID)
}

func (r *redisStreamReader) ack(ctx context.Context, id string) error {
	if err := r.client.XAck(ctx, r.cfg.Stream, r.group, id).Err(); err != nil && ctx.Err() == nil {
		return fmt.Errorf("XACK %q %s: %w", r.cfg.Stream, id, err)
	}
	return nil
}

// redisEntryData is one field's value, or every field as a JSON object.
func redisEntryData(entry redis.XMessage, field string) ([]byte, error) {
	var data []byte
	if field != "" {
		value, ok := entry.Values[field]
		if !ok {
			return nil, fmt.Errorf("entry has no field %q", field)
		}
		data = []byte(fmt.Sprint(value))
	} else {
		encoded, err := json.Marshal(entry.Values)
		if err != nil {
			return nil, err
		}
		data = encoded
	}
	if len(data) > MaxMessageBytes {
		return nil, fmt.Errorf("entry exceeds %d bytes", MaxMessageBytes)
	}
	return data, nil
}
