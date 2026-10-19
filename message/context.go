package message

import "context"

type sourceMetaKey struct{}

// WithSourceMeta attaches per-message metadata from a pulled source (for
// example kafka.partition or amqp.routingKey). The pipeline copies it into
// Meta of every message framed from that source message.
func WithSourceMeta(ctx context.Context, meta map[string]string) context.Context {
	if len(meta) == 0 {
		return ctx
	}
	return context.WithValue(ctx, sourceMetaKey{}, meta)
}

// SourceMeta returns the metadata WithSourceMeta attached, or nil.
func SourceMeta(ctx context.Context) map[string]string {
	meta, _ := ctx.Value(sourceMetaKey{}).(map[string]string)
	return meta
}
