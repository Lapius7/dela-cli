package main

import (
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 遅延ヒストグラムの境界(秒)。Prometheus クライアントの既定値に近い値
var latencyBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// metrics は --metrics-port のために数える値。標準ライブラリだけで Prometheus のテキスト形式を書き出す。
type metrics struct {
	mu         sync.Mutex
	requests   uint64
	byStatus   map[int]uint64
	buckets    []uint64 // latencyBuckets の各境界以下の件数(累積にせず、書き出すときに累積する)
	durSum     float64
	durCount   uint64
	connected  bool
	reconnects uint64
	startedAt  time.Time
}

func newMetrics() *metrics {
	return &metrics{byStatus: map[int]uint64{}, buckets: make([]uint64, len(latencyBuckets)), startedAt: time.Now()}
}

// observe は1リクエスト分(ステータスと所要時間)を記録する
func (m *metrics) observe(status int, d time.Duration) {
	if m == nil {
		return
	}
	sec := d.Seconds()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests++
	m.byStatus[status]++
	m.durSum += sec
	m.durCount++
	for i, le := range latencyBuckets {
		if sec <= le {
			m.buckets[i]++
			break
		}
	}
}

// setConnected はトンネルの接続状態を更新する。
// 最初の接続は再接続に数えず、いったん確立したあとの再確立だけを reconnects に加える。
func (m *metrics) setConnected(up, reconnect bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connected = up
	if up && reconnect {
		m.reconnects++
	}
}

// render は Prometheus のテキスト形式(version 0.0.4)で書き出す
func (m *metrics) render() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var b strings.Builder
	b.WriteString("# HELP dela_requests_total Total HTTP requests handled by the local proxy.\n# TYPE dela_requests_total counter\n")
	fmt.Fprintf(&b, "dela_requests_total %d\n", m.requests)

	b.WriteString("# HELP dela_requests_by_status_total HTTP requests by response status code.\n# TYPE dela_requests_by_status_total counter\n")
	codes := make([]int, 0, len(m.byStatus))
	for c := range m.byStatus {
		codes = append(codes, c)
	}
	sort.Ints(codes)
	for _, c := range codes {
		fmt.Fprintf(&b, "dela_requests_by_status_total{code=\"%d\"} %d\n", c, m.byStatus[c])
	}

	b.WriteString("# HELP dela_request_duration_seconds Time from receiving a request to finishing the response.\n# TYPE dela_request_duration_seconds histogram\n")
	var cum uint64
	for i, le := range latencyBuckets {
		cum += m.buckets[i]
		fmt.Fprintf(&b, "dela_request_duration_seconds_bucket{le=\"%s\"} %d\n", strconv.FormatFloat(le, 'g', -1, 64), cum)
	}
	fmt.Fprintf(&b, "dela_request_duration_seconds_bucket{le=\"+Inf\"} %d\n", m.durCount)
	fmt.Fprintf(&b, "dela_request_duration_seconds_sum %s\n", strconv.FormatFloat(m.durSum, 'g', -1, 64))
	fmt.Fprintf(&b, "dela_request_duration_seconds_count %d\n", m.durCount)

	up := 0
	if m.connected {
		up = 1
	}
	b.WriteString("# HELP dela_tunnel_connected 1 while the tunnel is connected, 0 otherwise.\n# TYPE dela_tunnel_connected gauge\n")
	fmt.Fprintf(&b, "dela_tunnel_connected %d\n", up)
	b.WriteString("# HELP dela_reconnects_total Number of times the tunnel was re-established after a drop.\n# TYPE dela_reconnects_total counter\n")
	fmt.Fprintf(&b, "dela_reconnects_total %d\n", m.reconnects)
	b.WriteString("# HELP dela_start_time_seconds Unix time when dela started.\n# TYPE dela_start_time_seconds gauge\n")
	fmt.Fprintf(&b, "dela_start_time_seconds %d\n", m.startedAt.Unix())
	return b.String()
}

// metricsHandler は /metrics だけを返す。DNS リバインディング対策として、Host が自分のアドレスのときだけ応答する(inspect と同じ)。
func metricsHandler(m *metrics) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(m.render()))
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			h = r.Host
		}
		if h != "127.0.0.1" && h != "localhost" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

type metricsServer struct {
	srv  *http.Server
	Addr string
}

// startMetrics は 127.0.0.1:<port> だけで待ち受ける(外部には公開しない)
func startMetrics(m *metrics, port int) (*metricsServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return nil, err
	}
	s := &metricsServer{Addr: ln.Addr().String()}
	s.srv = &http.Server{Handler: metricsHandler(m), ReadHeaderTimeout: 10 * time.Second}
	go s.srv.Serve(ln)
	return s, nil
}

func (s *metricsServer) close() { _ = s.srv.Close() }
