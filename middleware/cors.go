package middleware

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gozarp/zarp"
)

// CORSConfig configures CORS.
type CORSConfig struct {
	// AllowOrigins lists the origins allowed to read responses. A single "*"
	// allows any origin, which cannot be combined with AllowCredentials.
	//
	// An entry is an origin, not a URL: scheme and host, optionally a port, and
	// nothing else. "https://app.example.com" matches; "https://app.example.com/"
	// never can, because a browser never sends a trailing slash in Origin, so a
	// malformed entry is rejected at construction rather than silently ignored
	// for the life of the process.
	AllowOrigins []string

	// AllowOriginFunc decides per request, for cases a list cannot express.
	// It is consulted when AllowOrigins does not already match.
	AllowOriginFunc func(origin string) bool

	// AllowMethods and AllowHeaders answer a preflight. Defaults cover the
	// usual verbs and Content-Type/Authorization.
	AllowMethods []string
	AllowHeaders []string

	// ExposeHeaders lists response headers scripts may read beyond the safe set.
	ExposeHeaders []string

	// AllowCredentials lets the browser send cookies and HTTP auth.
	AllowCredentials bool

	// MaxAge is how long a preflight result may be cached.
	MaxAge time.Duration
}

// CORS returns middleware allowing any origin, without credentials.
//
// That combination is the only safe permissive default: a wildcard origin with
// credentials would let any site on the internet make authenticated requests as
// your users, which is why the browsers reject it and why this package refuses
// to configure it.
func CORS() zarp.HandlerFunc {
	return CORSWithConfig(CORSConfig{AllowOrigins: []string{"*"}})
}

// CORSWithConfig returns a configured CORS.
//
// Every header value is built here, at construction, rather than joined per
// request — a request only picks strings that already exist.
func CORSWithConfig(cfg CORSConfig) zarp.HandlerFunc {
	allowAll := false
	origins := make([]string, 0, len(cfg.AllowOrigins))
	for _, o := range cfg.AllowOrigins {
		if o == "*" {
			allowAll = true
			continue
		}
		if err := checkOrigin(o); err != nil {
			panic("zarp: CORS " + err.Error())
		}
		origins = append(origins, o)
	}

	if allowAll && cfg.AllowCredentials {
		panic("zarp: CORS cannot allow all origins with credentials — " +
			"list the origins you trust instead")
	}

	methods := cfg.AllowMethods
	if len(methods) == 0 {
		methods = []string{
			http.MethodGet, http.MethodPost, http.MethodPut,
			http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions,
		}
	}
	headers := cfg.AllowHeaders
	if len(headers) == 0 {
		headers = []string{"Origin", "Content-Type", "Accept", "Authorization"}
	}

	var (
		allowMethods  = strings.Join(methods, ", ")
		allowHeaders  = strings.Join(headers, ", ")
		exposeHeaders = strings.Join(cfg.ExposeHeaders, ", ")
		maxAge        string
	)
	if cfg.MaxAge > 0 {
		maxAge = strconv.FormatInt(int64(cfg.MaxAge/time.Second), 10)
	}

	allowed := func(origin string) bool {
		if allowAll {
			return true
		}
		for _, o := range origins {
			if o == origin {
				return true
			}
		}
		return cfg.AllowOriginFunc != nil && cfg.AllowOriginFunc(origin)
	}

	return func(c *zarp.Context) {
		origin := c.GetHeader("Origin")
		if origin == "" {
			// Not a cross-origin request; nothing to negotiate.
			c.Next()
			return
		}

		// The response varies by Origin whether or not this one is allowed, or
		// a shared cache will serve one origin's answer to another.
		c.Writer.Header().Add("Vary", "Origin")

		if !allowed(origin) {
			// Send no CORS headers: the browser blocks the read, which is the
			// correct outcome. A preflight still ends here.
			if isPreflight(c) {
				c.AbortWithStatus(http.StatusNoContent)
				return
			}
			c.Next()
			return
		}

		if allowAll && !cfg.AllowCredentials {
			c.Header("Access-Control-Allow-Origin", "*")
		} else {
			// Echoing the origin is only safe because it was matched above.
			c.Header("Access-Control-Allow-Origin", origin)
		}
		if cfg.AllowCredentials {
			c.Header("Access-Control-Allow-Credentials", "true")
		}
		if exposeHeaders != "" {
			c.Header("Access-Control-Expose-Headers", exposeHeaders)
		}

		if !isPreflight(c) {
			c.Next()
			return
		}

		c.Writer.Header().Add("Vary", "Access-Control-Request-Method")
		c.Writer.Header().Add("Vary", "Access-Control-Request-Headers")
		c.Header("Access-Control-Allow-Methods", allowMethods)
		c.Header("Access-Control-Allow-Headers", allowHeaders)
		if maxAge != "" {
			c.Header("Access-Control-Max-Age", maxAge)
		}
		// A preflight is answered here and never reaches a route.
		c.AbortWithStatus(http.StatusNoContent)
	}
}

// checkOrigin rejects anything a browser could never send in an Origin header,
// because such an entry can only ever fail to match.
func checkOrigin(origin string) error {
	if origin == "null" {
		return errors.New(`cannot allow the "null" origin: it is sent by sandboxed ` +
			"documents of any provenance, so it identifies nothing — use AllowOriginFunc " +
			"if you really mean to accept it")
	}

	u, err := url.Parse(origin)
	if err != nil {
		return fmt.Errorf("origin %q is not parseable: %w", origin, err)
	}
	switch {
	case u.Scheme == "":
		return fmt.Errorf("origin %q has no scheme, e.g. https://%s", origin, origin)
	case u.Host == "":
		return fmt.Errorf("origin %q has no host", origin)
	case u.Path != "":
		return fmt.Errorf("origin %q has a path: an Origin header carries scheme and host only", origin)
	case u.RawQuery != "" || u.Fragment != "":
		return fmt.Errorf("origin %q has a query or fragment: an Origin header carries scheme and host only", origin)
	case u.User != nil:
		return fmt.Errorf("origin %q has userinfo: an Origin header carries scheme and host only", origin)
	case u.Host != strings.ToLower(u.Host):
		// A browser serialises the origin lowercased, so a configured
		// "https://Example.com" is a rule that can never fire.
		return fmt.Errorf("origin %q must be lowercase: browsers send %q",
			origin, u.Scheme+"://"+strings.ToLower(u.Host))
	}
	return nil
}

func isPreflight(c *zarp.Context) bool {
	return c.Request.Method == http.MethodOptions &&
		c.GetHeader("Access-Control-Request-Method") != ""
}
