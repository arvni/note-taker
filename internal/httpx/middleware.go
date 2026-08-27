package httpx

import "net/http"

// Middleware wraps an http.HandlerFunc (e.g. rate limiting).
type Middleware func(http.HandlerFunc) http.HandlerFunc

// chain returns a function applying mw if non-nil, else the identity.
func chain(mw Middleware) Middleware {
	if mw == nil {
		return func(h http.HandlerFunc) http.HandlerFunc { return h }
	}
	return mw
}
