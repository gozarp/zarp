package binding

import (
	"net/http"

	"github.com/gozarp/zarp"
)

// headerBinding reads request headers using `header` tags. Lookups are
// canonicalised, so a tag of "x-request-id" matches "X-Request-Id".
type headerBinding struct{}

func (headerBinding) Name() string { return "header" }

func (headerBinding) Bind(c *zarp.Context, obj any) error {
	return mapSource(obj, "header", func(key string) ([]string, bool) {
		if c.Request == nil {
			return nil, false
		}
		values, ok := c.Request.Header[http.CanonicalHeaderKey(key)]
		return values, ok && len(values) > 0
	})
}
