// Copyright 2026 Subhanjan Adhikary. All rights reserved.
// Use of this source code is governed by a MIT style
// license that can be found in the LICENSE file.

package binding

import "github.com/gozarp/zarp"

// uriBinding reads the route parameters matched by the router — the ":id" in
// "/users/:id" — using `uri` tags.
type uriBinding struct{}

func (uriBinding) Name() string { return "uri" }

func (uriBinding) Bind(c *zarp.Context, obj any) error {
	return mapSource(obj, "uri", func(key string) ([]string, bool) {
		v, ok := c.Params.Get(key)
		if !ok {
			return nil, false
		}
		return []string{v}, true
	})
}
