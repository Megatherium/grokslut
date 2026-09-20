package auth_test

import (
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"

	"github.com/Megatherium/grokslut/auth"
)

func TestApplyRequiresCookieJar(t *testing.T) {
	err := (auth.Session{}).Apply(&http.Client{}, "https://grok.com")
	if err == nil || !strings.Contains(err.Error(), "cookie jar") {
		t.Fatalf("expected cookie-jar error, got %v", err)
	}
}

func TestApplyRejectsPublicSuffixLikeCookieDomain(t *testing.T) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	session := auth.Session{Cookies: []auth.Cookie{{Name: "sso", Value: "private", Domain: "com"}}}
	err = session.Apply(&http.Client{Jar: jar}, "https://grok.com")
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected domain mismatch, got %v", err)
	}
}

func TestApplyAllowsMatchingParentCookieDomain(t *testing.T) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	session := auth.Session{Cookies: []auth.Cookie{{Name: "session", Value: "private", Domain: ".google.com"}}}
	if err := session.Apply(&http.Client{Jar: jar}, "https://gemini.google.com"); err != nil {
		t.Fatal(err)
	}
}
