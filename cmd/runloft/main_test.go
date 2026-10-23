package main

import (
	"net/url"
	"testing"
)

func TestSafeExternalURLRejectsCredentialsAndStripsTokens(t *testing.T) {
	for _, input := range []string{
		"file:///etc/passwd",
		"javascript:alert(1)",
		"https://user:secret@example.com/path",
	} {
		if _, ok := safeExternalURL(input); ok {
			t.Errorf("safeExternalURL(%q) accepted an unsafe URL", input)
		}
	}
	got, ok := safeExternalURL("https://example.com/path?keep=yes&access_token=secret#private")
	if !ok {
		t.Fatal("safe external HTTPS URL was rejected")
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("keep") != "yes" || parsed.Query().Has("access_token") || parsed.Fragment != "" {
		t.Fatalf("sensitive parts were not removed: %s", got)
	}
}
