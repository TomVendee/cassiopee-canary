package main

import (
	"net/http"
	"testing"
)

func TestMaskEnv(t *testing.T) {
	got := MaskEnv([]string{
		"DB_PASSWORD=x", "API_KEY=x", "GITHUB_TOKEN=x", "MY_SECRET=x", "CANARY_TOKEN=x",
		"HELLO=world",
		"DATABASE_URL=postgresql://u:pw@h:5432/app",
		"EMPTY=",
		"WEIRD",
	})
	want := map[string]string{
		"DB_PASSWORD": Masked, "API_KEY": Masked, "GITHUB_TOKEN": Masked, "MY_SECRET": Masked, "CANARY_TOKEN": Masked,
		"HELLO":        "world",
		"DATABASE_URL": "postgresql://u:" + Masked + "@h:5432/app",
		"EMPTY":        "",
	}
	if len(got) != len(want) {
		t.Errorf("obtenu %d variables, attendu %d : %v", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, attendu %q", k, got[k], v)
		}
	}
}

func TestMaskURL(t *testing.T) {
	tests := map[string]string{
		"not a url":                             "not a url",
		"mongodb://u:pw@h/app?authSource=admin": "mongodb://u:" + Masked + "@h/app?authSource=admin",
		"https://example.com/path":              "https://example.com/path",
		"mysql://u@h:3306/app":                  "mysql://u@h:3306/app",
	}
	for in, want := range tests {
		if got := MaskURL(in); got != want {
			t.Errorf("MaskURL(%q) = %q, attendu %q", in, got, want)
		}
	}
}

func TestMaskHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer abc")
	h.Set("Cookie", "session=abc")
	h.Add("X-Forwarded-For", "1.2.3.4")
	h.Add("X-Forwarded-For", "5.6.7.8")
	got := MaskHeaders(h)
	if got["Authorization"] != Masked || got["Cookie"] != Masked {
		t.Errorf("en-têtes sensibles non masqués : %v", got)
	}
	if got["X-Forwarded-For"] != "1.2.3.4, 5.6.7.8" {
		t.Errorf("X-Forwarded-For = %q", got["X-Forwarded-For"])
	}
}
