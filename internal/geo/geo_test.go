package geo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLookupParsesChinese(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/198.18.0.1") || r.URL.Query().Get("lang") != "zh-CN" {
			t.Errorf("url=%s", r.URL)
		}
		_, _ = w.Write([]byte(`{"status":"success","country":"日本","countryCode":"jp","city":"东京"}`))
	}))
	defer srv.Close()
	old := Endpoint
	Endpoint = srv.URL + "/json/"
	defer func() { Endpoint = old }()

	info, err := Lookup(context.Background(), "198.18.0.1", "")
	if err != nil {
		t.Fatal(err)
	}
	if info.Code != "JP" || info.Place() != "日本 · 东京" {
		t.Fatalf("info=%+v", info)
	}
}

func TestLookupFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"fail","message":"private range"}`))
	}))
	defer srv.Close()
	old := Endpoint
	Endpoint = srv.URL + "/json/"
	defer func() { Endpoint = old }()
	if _, err := Lookup(context.Background(), "10.0.0.1", ""); err == nil {
		t.Fatal("expected error")
	}
}

func TestPlace(t *testing.T) {
	if (Info{Country: "新加坡", City: "新加坡"}).Place() != "新加坡" {
		t.Fatal("city equal to country should not repeat")
	}
}
