// Copyright 2026 Subhanjan Adhikary. All rights reserved.
// Use of this source code is governed by a MIT style
// license that can be found in the LICENSE file.

package binding

import (
	"fmt"

	"github.com/gozarp/zarp"
)

// queryBinding reads the URL query string. It goes through the Context's own
// accessors so the query is parsed once per request, not once per binder.
type queryBinding struct{}

func (queryBinding) Name() string { return "query" }

func (queryBinding) Bind(c *zarp.Context, obj any) error {
	return mapSource(obj, "form", c.GetQueryArray)
}

// formBinding reads a urlencoded body.
type formBinding struct{}

func (formBinding) Name() string { return "form" }

func (formBinding) Bind(c *zarp.Context, obj any) error {
	// Ask the Context to parse first, so a body that failed to parse is an
	// error here rather than a struct full of zero values.
	if err := c.FormError(); err != nil {
		return fmt.Errorf("binding: %w", err)
	}
	return mapSource(obj, "form", c.GetPostFormArray)
}

// multipartBinding reads the value fields of a multipart body. Uploaded files
// are not bound: read them with Context.FormFile, which hands back the header
// without copying the file into memory.
type multipartBinding struct{}

func (multipartBinding) Name() string { return "multipart" }

func (multipartBinding) Bind(c *zarp.Context, obj any) error {
	if _, err := c.MultipartForm(); err != nil {
		return fmt.Errorf("binding: %w", err)
	}
	if err := c.FormError(); err != nil {
		return fmt.Errorf("binding: %w", err)
	}
	return mapSource(obj, "form", c.GetPostFormArray)
}
