package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestBuildURL(t *testing.T) {
	params := url.Values{}
	params.Set("includedTags", "maid office")

	got := buildURL("https://api.example/v1/", "/images", params)
	want := "https://api.example/v1/images?includedTags=maid+office"
	if got != want {
		t.Fatalf("buildURL = %q, want %q", got, want)
	}
}

func TestGetJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("includedTags"); got != "maid" {
			t.Errorf("includedTags = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"items":[{"id":1}]}`)
	}))
	defer server.Close()

	client := newAPIClient(&config{apiBase: server.URL}, newAppState())
	params := url.Values{}
	params.Set("includedTags", "maid")

	body, err := client.getJSON(server.URL, "images", params, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	items := getList(body, "items")
	if len(items) != 1 || getInt(items[0], "id") != 1 {
		t.Fatalf("items = %#v", items)
	}
}

func TestGetJSONProblemDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"detail":"no such image"}`)
	}))
	defer server.Close()

	client := newAPIClient(&config{apiBase: server.URL}, newAppState())
	_, err := client.getJSON(server.URL, "images/999", nil, time.Second)

	var apiErr *apiError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *apiError", err)
	}
	if apiErr.Status != http.StatusNotFound {
		t.Errorf("status = %d", apiErr.Status)
	}
	if apiErr.Detail != "no such image" {
		t.Errorf("detail = %q", apiErr.Detail)
	}
}

func TestRegisterInstance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/instances" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("X-Api-Key"); got != "secret" {
			t.Errorf("X-Api-Key = %q", got)
		}

		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode: %v", err)
		}
		if payload["url"] != "https://waifu.example" {
			t.Errorf("url = %#v", payload["url"])
		}
		if payload["name"] != "My Instance" {
			t.Errorf("name = %#v", payload["name"])
		}
		if payload["discordUserId"] != "123" {
			t.Errorf("discordUserId = %#v", payload["discordUserId"])
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"name":"My Instance","url":"https://waifu.example"}`)
	}))
	defer server.Close()

	client := newAPIClient(&config{registerBase: server.URL, adminKey: "secret"}, newAppState())
	status, body, err := client.registerInstance("https://waifu.example", "My Instance", "123")
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusCreated {
		t.Errorf("status = %d", status)
	}
	if getString(body, "name") != "My Instance" {
		t.Errorf("body = %#v", body)
	}
}

func TestAPIProblemDetailPlainText(t *testing.T) {
	if got := apiProblemDetail(http.StatusBadGateway, []byte("upstream failed")); got != "upstream failed" {
		t.Errorf("got %q", got)
	}
	if got := apiProblemDetail(http.StatusBadGateway, []byte(`{"unexpected":true}`)); got != "The waifu API returned HTTP 502." {
		t.Errorf("got %q", got)
	}
}
