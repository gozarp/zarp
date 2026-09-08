package middleware

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/subhanjanops/gomicro"
)

// CORSConfig configures CORS.
type CORSConfig struct {
	// AllowOrigins lists the origins allowed to read responses. A single "*"
	// allows any origin, which cannot be combined with AllowCredentials.
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
func CORS() gomicro.HandlerFunc {
	return CORSWithConfig(CORSConfig{AllowOrigins: []string{"*"}})
}

// CORSWithConfig returns a configured CORS.
//
// Every header value is built here, at construction, rather than joined per
// request — a request only picks strings that already exist.
func CORSWithConfig(cfg CORSConfig) gomicro.HandlerFunc {
	allowAll := false
	origins := make([]string, 0, len(cfg.AllowOrigins))
	for _, o := range cfg.AllowOrigins {
		if o == "*" {
			allowAll = true
			continue
		}
		origins = append(origins, o)
	}

	if allowAll && cfg.AllowCredentials {
		panic("gomicro: CORS cannot allow all origins with credentials — " +
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

	return func(c *gomicro.Context) {
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

func isPreflight(c *gomicro.Context) bool {
	return c.Request.Method == http.MethodOptions &&
		c.GetHeader("Access-Control-Request-Method") != ""
}
