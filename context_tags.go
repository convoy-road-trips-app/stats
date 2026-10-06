package stats

import (
	"context"
	"slices"
	"sync"

	"go.opentelemetry.io/otel/attribute"
)

// contextTagsKey is the context key under which a *contextTags is stored.
type contextTagsKey struct{}

// contextTags is the mutable tag set carried by a context. Its mutex makes
// ContextAddTags safe to call from goroutines that share the context.
type contextTags struct {
	mu   sync.RWMutex
	tags []attribute.KeyValue
}

// ContextWithTags returns a copy of ctx that carries tags. Every metric
// recorded with the returned context, or a context derived from it, includes
// them.
//
// The tags are not inherited from ctx: if ctx already carries tags, they are
// replaced by tags rather than merged. Pass the previous ContextTags result
// along with the new tags to keep them.
//
// Context tags are merged after any view tags and before the metric's own
// attributes and explicit options, so an explicit option wins on a duplicate
// key. They pass the same key validation and cardinality limits as option
// tags, so an invalid key makes the recording fail with ErrInvalidTagKey.
//
// Tags become metric series dimensions. Use only low-cardinality values such
// as a region, tenant tier or route template; never request IDs, user IDs or
// other unbounded values.
func ContextWithTags(ctx context.Context, tags ...attribute.KeyValue) context.Context {
	return context.WithValue(ctx, contextTagsKey{}, &contextTags{tags: slices.Clone(tags)})
}

// ContextAddTags appends tags to the tag set that ctx carries, in place, so
// the change is visible to every context sharing that tag set. It is safe for
// concurrent use. It reports false, and does nothing, when ctx was not created
// by ContextWithTags and so has no tag set to add to.
//
// The same cardinality rules as ContextWithTags apply: do not add request IDs
// or other unbounded values.
func ContextAddTags(ctx context.Context, tags ...attribute.KeyValue) bool {
	ct, ok := ctx.Value(contextTagsKey{}).(*contextTags)
	if !ok {
		return false
	}
	ct.mu.Lock()
	ct.tags = append(ct.tags, tags...)
	ct.mu.Unlock()
	return true
}

// ContextTags returns a copy of the tags that ctx carries, or nil when it has
// none. Modifying the result does not affect the context.
func ContextTags(ctx context.Context) []attribute.KeyValue {
	ct, ok := ctx.Value(contextTagsKey{}).(*contextTags)
	if !ok {
		return nil
	}
	ct.mu.RLock()
	defer ct.mu.RUnlock()
	return slices.Clone(ct.tags)
}
