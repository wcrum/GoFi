package kubeclient

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PageSize is how many objects one list request asks for.
const PageSize = 500

// Page is one list call: it returns the items of the page opts selects and
// the continue token for the next page ("" when this was the last).
type Page[T any] func(ctx context.Context, opts metav1.ListOptions) (items []T, next string, err error)

// ListAll walks every page of a list up to limit items. It returns the
// items read and whether the listing stopped short (truncated), so callers
// can warn instead of silently showing a partial inventory. A limit of 0
// means no limit.
func ListAll[T any](ctx context.Context, limit int, selector string, page Page[T]) (items []T, truncated bool, err error) {
	opts := metav1.ListOptions{Limit: PageSize, LabelSelector: selector}
	for {
		if limit > 0 && len(items) >= limit {
			return items[:limit], true, nil
		}
		if limit > 0 && limit-len(items) < PageSize {
			opts.Limit = int64(limit - len(items))
		}
		batch, next, err := page(ctx, opts)
		if err != nil {
			return items, false, err
		}
		items = append(items, batch...)
		if next == "" {
			return items, false, nil
		}
		opts.Continue = next
	}
}

// TruncationWarning describes a listing that stopped at limit.
func TruncationWarning(kind string, limit int) string {
	return fmt.Sprintf("%s: more than %d objects; showing the first %d", kind, limit, limit)
}
