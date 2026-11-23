package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetricsRender(t *testing.T) {
	m := newMetrics()
	m.observe(200, 20*time.Millisecond)
	m.observe(200, 3*time.Second)
	m.observe(502, 1*time.Millisecond)
	m.setConnected(true, false)
	m.setConnected(false, false)
	m.setConnected(true, true)
	got := m.render()
	for _, want := range []string{
		"dela_requests_total 3\n",
		`dela_requests_by_status_total{code="200"} 2`,
		`dela_requests_by_status_total{code="502"} 1`,
		`dela_request_duration_seconds_bucket{le="0.005"} 1`,
		`dela_request_duration_seconds_bucket{le="0.025"} 2`, // 累積: 1ms と 20ms
		`dela_request_duration_seconds_bucket{le="5"} 3`,
		`dela_request_duration_seconds_bucket{le="+Inf"} 3`,
		"dela_request_duration_seconds_count 3\n",
		"dela_tunnel_connected 1\n",
		"dela_reconnects_total 1\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("出力に %q がありません:\n%s", want, got)
		}
	}
	// 最初の接続は再接続に数えない
	m2 := newMetrics()
	m2.setConnected(true, false)
	if !strings.Contains(m2.render(), "dela_reconnects_total 0\n") {
		t.Error("最初の接続は reconnects に数えない")
	}
}

func TestMetricsNilSafe(t *testing.T) {
	var m *metrics
	m.observe(200, time.Millisecond)
	m.setConnected(true, true)
}

func TestMetricsHandlerHostGuard(t *testing.T) {
	h := metricsHandler(newMetrics())
	get := func(host, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://"+host+path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := get("127.0.0.1:9100", "/metrics"); w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
		t.Errorf("%d %v", w.Code, w.Header())
	}
	if w := get("evil.example:9100", "/metrics"); w.Code != http.StatusForbidden {
		t.Errorf("他の Host は拒否: %d", w.Code)
	}
	if w := get("127.0.0.1:9100", "/other"); w.Code != http.StatusNotFound {
		t.Errorf("/metrics 以外は 404: %d", w.Code)
	}
}

func TestMetricsServerLoopbackAndProxy(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/boom" {
			w.WriteHeader(500)
			return
		}
		io.WriteString(w, "ok")
	}))
	defer backend.Close()
	m := newMetrics()
	px, err := startProxy(strings.TrimPrefix(backend.URL, "http://"), options{}, m)
	if err != nil {
		t.Fatal(err)
	}
	defer px.close()
	for _, p := range []string{"/", "/", "/boom"} {
		resp, err := http.Get("http://" + px.addr + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	ms, err := startMetrics(m, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer ms.close()
	if !strings.HasPrefix(ms.Addr, "127.0.0.1:") {
		t.Fatalf("127.0.0.1 だけで待ち受ける: %s", ms.Addr)
	}
	resp, err := http.Get("http://" + ms.Addr + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	body := string(b)
	for _, want := range []string{"dela_requests_total 3\n", `code="200"} 2`, `code="500"} 1`} {
		if !strings.Contains(body, want) {
			t.Errorf("%q がありません:\n%s", want, body)
		}
	}
}
