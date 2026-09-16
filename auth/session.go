// Package auth models the authenticated browser session used by Grok's private
// chat-history endpoints. It never persists or prints secrets itself.
package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// Cookie is the JSON-safe envelope supplied by a first-party login surface.
type Cookie struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Domain string `json:"domain"`
}

// Session contains a browser cookie jar and only the request headers needed by
// the site. Header values are intentionally absent from Summary.
type Session struct {
	Cookies []Cookie          `json:"cookies"`
	Headers map[string]string `json:"headers"`
}

type Summary struct {
	Cookies []CookieSummary `json:"cookies"`
	Headers []string        `json:"headers"`
}

type CookieSummary struct {
	Name   string `json:"name"`
	Domain string `json:"domain"`
}

// FromJSON accepts {cookies,headers}, or a bare cookie array for FFI callers.
func FromJSON(raw []byte) (Session, error) {
	var session Session
	if err := json.Unmarshal(raw, &session); err != nil {
		var cookies []Cookie
		if arrayErr := json.Unmarshal(raw, &cookies); arrayErr != nil {
			return Session{}, fmt.Errorf("decode session: %w", err)
		}
		session.Cookies = cookies
	}
	if session.Headers == nil {
		session.Headers = map[string]string{}
	}
	for i := range session.Cookies {
		cookie := &session.Cookies[i]
		if cookie.Name == "" || cookie.Value == "" {
			return Session{}, fmt.Errorf("each cookie needs a name and value")
		}
		if cookie.Domain == "" {
			cookie.Domain = ".grok.com"
		}
		cookie.Domain = strings.TrimSuffix(strings.TrimPrefix(cookie.Domain, "https://"), "/")
	}
	return session, nil
}

func FromFile(name string) (Session, error) {
	raw, err := os.ReadFile(name)
	if err != nil {
		return Session{}, err
	}
	return FromJSON(raw)
}

func (s Session) Apply(client *http.Client, baseURL string) error {
	base, err := url.Parse(baseURL)
	if err != nil {
		return err
	}
	baseHost := strings.ToLower(base.Hostname())
	if baseHost == "" {
		return fmt.Errorf("base URL needs a host")
	}
	for _, cookie := range s.Cookies {
		domain := strings.TrimPrefix(cookie.Domain, ".")
		cookieHost := strings.ToLower(domain)
		if cookieHost == "" {
			cookieHost = baseHost
		}
		if host, _, found := strings.Cut(cookieHost, ":"); found {
			cookieHost = host
		}
		if cookieHost != baseHost && !strings.HasSuffix(baseHost, "."+cookieHost) {
			return fmt.Errorf("cookie domain %q does not match %s", cookie.Domain, baseHost)
		}
		target := *base
		if domain != "" {
			target.Host = domain
		}
		client.Jar.SetCookies(&target, []*http.Cookie{{Name: cookie.Name, Value: cookie.Value, Domain: cookie.Domain, Path: "/", Secure: target.Scheme == "https"}})
	}
	return nil
}

func (s Session) Summary() Summary {
	result := Summary{Headers: make([]string, 0, len(s.Headers))}
	for _, cookie := range s.Cookies {
		result.Cookies = append(result.Cookies, CookieSummary{Name: cookie.Name, Domain: cookie.Domain})
	}
	for header := range s.Headers {
		result.Headers = append(result.Headers, header)
	}
	return result
}
