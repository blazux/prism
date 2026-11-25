// Package httpguard keeps authenticated API redirects within their original origin.
package httpguard

import (
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// ErrRedirect identifies a permanent redirect-policy refusal, which must not be retried.
var ErrRedirect = errors.New("HTTP redirect blocked")

// SameOriginRedirect retains net/http's ten-request limit and permits path
// canonicalization without forwarding credentials or bodies to another origin.
func SameOriginRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("%w: stopped after 10 redirects", ErrRedirect)
	}
	if len(via) == 0 || req == nil || via[0] == nil || !sameOrigin(req.URL, via[0].URL) {
		return fmt.Errorf("%w: destination must use the configured server origin", ErrRedirect)
	}
	return nil
}

type origin struct{ scheme, host, port string }

func canonicalOrigin(u *url.URL) (origin, bool) {
	if u == nil {
		return origin{}, false
	}
	o := origin{scheme: strings.ToLower(u.Scheme), host: u.Hostname(), port: u.Port()}
	if o.host == "" || (o.scheme != "http" && o.scheme != "https") {
		return origin{}, false
	}
	if address, err := netip.ParseAddr(o.host); err == nil {
		o.host = address.String()
	} else {
		o.host = strings.ToLower(o.host)
	}
	if o.port == "" {
		o.port = "80"
		if o.scheme == "https" {
			o.port = "443"
		}
	} else {
		port, err := strconv.ParseUint(o.port, 10, 16)
		if err != nil {
			return origin{}, false
		}
		o.port = strconv.FormatUint(port, 10)
	}
	return o, true
}

func sameOrigin(left, right *url.URL) bool {
	a, validA := canonicalOrigin(left)
	b, validB := canonicalOrigin(right)
	return validA && validB && a == b
}

// Do removes net/http's URL wrapper from our policy errors: a redirect's
// Location may itself contain credentials or other private query parameters.
func Do(client *http.Client, req *http.Request) (*http.Response, error) {
	response, err := client.Do(req)
	if errors.Is(err, ErrRedirect) {
		var redirect *url.Error
		if errors.As(err, &redirect) {
			err = redirect.Err
		}
	}
	return response, err
}
