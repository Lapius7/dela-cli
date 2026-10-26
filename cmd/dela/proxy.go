package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

//go:embed inspect.html
var inspectHTML []byte

const (
	maxCapture  = 64 << 10 // リクエスト/レスポンス本文を記録する上限(各 64KB)
	maxRequests = 300      // 保持するリクエスト数(古いものから捨てる)
)

// capturedReq は inspect 画面に出す1リクエスト分の記録
type capturedReq struct {
	ID          int         `json:"id"`
	Time        time.Time   `json:"time"`
	Method      string      `json:"method"`
	Path        string      `json:"path"`
	Remote      string      `json:"remote"`
	Status      int         `json:"status"`
	DurationMs  int64       `json:"durationMs"`
	ReqSize     int64       `json:"reqSize"`
	RespSize    int64       `json:"respSize"`
	ReqHeaders  http.Header `json:"reqHeaders"`
	RespHeaders http.Header `json:"respHeaders"`
	ReqBody     []byte      `json:"-"`
	RespBody    []byte      `json:"-"`
	ReqTrunc    bool        `json:"reqTruncated"`
	RespTrunc   bool        `json:"respTruncated"`
	Note        string      `json:"note,omitempty"`
}

type requestStore struct {
	mu    sync.Mutex
	items []*capturedReq
	next  int
}

func (s *requestStore) add(r *capturedReq) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	r.ID = s.next
	s.items = append(s.items, r)
	if len(s.items) > maxRequests {
		s.items = s.items[len(s.items)-maxRequests:]
	}
}

func (s *requestStore) get(id int) *capturedReq {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.items {
		if r.ID == id {
			return r
		}
	}
	return nil
}

func (s *requestStore) list() []*capturedReq {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*capturedReq, len(s.items))
	for i, r := range s.items {
		out[len(s.items)-1-i] = r // 新しい順
	}
	return out
}

func (s *requestStore) clear() {
	s.mu.Lock()
	s.items = nil
	s.mu.Unlock()
}

// 認証情報など、画面に出してはいけないヘッダーを伏せたコピーを作る
func redact(h http.Header) http.Header {
	c := h.Clone()
	for _, k := range []string{"Authorization", "Cookie", "Set-Cookie", "Proxy-Authorization"} {
		if _, ok := c[k]; ok {
			c[k] = []string{"[redacted]"}
		}
	}
	return c
}

// 先頭 maxCapture バイトだけ控えながら読み進める
type capBody struct {
	rc    io.ReadCloser
	buf   bytes.Buffer
	size  int64
	trunc bool
}

func (c *capBody) Read(p []byte) (int, error) {
	n, err := c.rc.Read(p)
	if n > 0 {
		c.size += int64(n)
		if room := maxCapture - c.buf.Len(); room > 0 {
			if n < room {
				room = n
			}
			c.buf.Write(p[:room])
		}
		if c.buf.Len() >= maxCapture && c.size > maxCapture {
			c.trunc = true
		}
	}
	return n, err
}

func (c *capBody) Close() error { return c.rc.Close() }

// ステータスとレスポンス本文を控える ResponseWriter(SSE の Flush と WebSocket の Hijack は素通し)
type recWriter struct {
	http.ResponseWriter
	status int
	hdr    http.Header
	buf    bytes.Buffer
	size   int64
	trunc  bool
}

func (w *recWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
		w.hdr = w.ResponseWriter.Header().Clone()
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *recWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(p)
	w.size += int64(n)
	if room := maxCapture - w.buf.Len(); room > 0 {
		if n < room {
			room = n
		}
		w.buf.Write(p[:room])
	}
	if w.size > maxCapture {
		w.trunc = true
	}
	return n, err
}

func (w *recWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *recWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijack not supported")
	}
	if w.status == 0 {
		w.status = http.StatusSwitchingProtocols
		w.hdr = w.ResponseWriter.Header().Clone()
	}
	return h.Hijack()
}

func (w *recWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// localProxy は公開側とローカルサーバーの間に入る小さなリバースプロキシ。
// Basic 認証(--auth)と、リクエストの記録・表示(--inspect)を担う。
type localProxy struct {
	rp      *httputil.ReverseProxy
	target  string
	authSum [32]byte
	useAuth bool
	inspect bool
	store   *requestStore
	srv     *http.Server
	addr    string
}

func startProxy(target string, o options) (*localProxy, error) {
	u := &url.URL{Scheme: "http", Host: target}
	p := &localProxy{target: target, inspect: o.inspect, store: &requestStore{}}
	if o.auth != "" {
		p.useAuth = true
		p.authSum = sha256.Sum256([]byte(o.auth))
	}
	p.rp = httputil.NewSingleHostReverseProxy(u)
	// Host ヘッダーは公開 URL のまま渡す(直接転送したときと同じ挙動)
	director := p.rp.Director
	p.rp.Director = func(r *http.Request) {
		host := r.Host
		director(r)
		r.Host = host
	}
	p.rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprintf(w, "502 Bad Gateway\nlocalhost 側のサーバー(%s)に接続できませんでした。起動しているか確認してください。\n", target)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p.addr = ln.Addr().String()
	p.srv = &http.Server{Handler: p, ReadHeaderTimeout: 30 * time.Second}
	go p.srv.Serve(ln)
	return p, nil
}

func (p *localProxy) close() { _ = p.srv.Close() }

func (p *localProxy) checkAuth(r *http.Request) bool {
	u, pw, ok := r.BasicAuth()
	if !ok {
		return false
	}
	sum := sha256.Sum256([]byte(u + ":" + pw))
	return subtle.ConstantTimeCompare(sum[:], p.authSum[:]) == 1
}

func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		return strings.TrimSpace(strings.Split(v, ",")[0])
	}
	h, _, _ := net.SplitHostPort(r.RemoteAddr)
	return h
}

func (p *localProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	rec := &capturedReq{
		Time:       start,
		Method:     r.Method,
		Path:       r.URL.RequestURI(),
		Remote:     clientIP(r),
		ReqHeaders: redact(r.Header),
	}
	rw := &recWriter{ResponseWriter: w}

	if p.useAuth && !p.checkAuth(r) {
		rw.Header().Set("WWW-Authenticate", `Basic realm="dela", charset="UTF-8"`)
		rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
		rw.WriteHeader(http.StatusUnauthorized)
		_, _ = rw.Write([]byte("401 Unauthorized\n"))
		rec.Note = "認証失敗"
		p.finish(rec, rw, nil, start)
		return
	}

	var cb *capBody
	if r.Body != nil && r.Body != http.NoBody {
		cb = &capBody{rc: r.Body}
		r.Body = cb
	}
	p.rp.ServeHTTP(rw, r)
	p.finish(rec, rw, cb, start)
}

func (p *localProxy) finish(rec *capturedReq, rw *recWriter, cb *capBody, start time.Time) {
	rec.Status = rw.status
	rec.DurationMs = time.Since(start).Milliseconds()
	rec.RespSize = rw.size
	rec.RespHeaders = redact(rw.hdr)
	rec.RespBody = rw.buf.Bytes()
	rec.RespTrunc = rw.trunc
	if cb != nil {
		rec.ReqSize = cb.size
		rec.ReqBody = cb.buf.Bytes()
		rec.ReqTrunc = cb.trunc
	}
	if !p.inspect {
		return
	}
	p.store.add(rec)
	colour := green
	switch {
	case rec.Status >= 500:
		colour = red
	case rec.Status >= 400:
		colour = yellow
	}
	out(fmt.Sprintf("  %s  %s %-6s %s  %s\n",
		dim(rec.Time.Format("15:04:05")), colour(strconv.Itoa(rec.Status)), rec.Method, rec.Path,
		dim(fmt.Sprintf("%dms · %s", rec.DurationMs, humanBytes(rec.RespSize)))))
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1fKB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%dB", n)
}

// ---- inspect 画面(ローカル専用) ----

type inspectServer struct {
	store *requestStore
	srv   *http.Server
	Addr  string
}

func startInspect(store *requestStore, port int) (*inspectServer, error) {
	var ln net.Listener
	var err error
	ports := []int{port}
	if port == 0 {
		ports = nil
		for p := 4040; p < 4060; p++ {
			ports = append(ports, p)
		}
	}
	for _, p := range ports {
		ln, err = net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p))
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	s := &inspectServer{store: store, Addr: ln.Addr().String()}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(inspectHTML)
	})
	mux.HandleFunc("/api/requests", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, store.list())
	})
	mux.HandleFunc("/api/requests/", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/api/requests/"))
		rec := store.get(id)
		if rec == nil {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, map[string]any{
			"request":  rec,
			"reqBody":  renderBody(rec.ReqBody, rec.ReqHeaders.Get("Content-Encoding"), rec.ReqTrunc, rec.ReqSize),
			"respBody": renderBody(rec.RespBody, rec.RespHeaders.Get("Content-Encoding"), rec.RespTrunc, rec.RespSize),
		})
	})
	mux.HandleFunc("/api/clear", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			store.clear()
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
	// DNS リバインディング対策: Host が自分のアドレスのときだけ応答する
	guard := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h, _, _ := net.SplitHostPort(r.Host)
			if h != "127.0.0.1" && h != "localhost" {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			next.ServeHTTP(w, r)
		})
	}
	s.srv = &http.Server{Handler: guard(mux), ReadHeaderTimeout: 10 * time.Second}
	go s.srv.Serve(ln)
	return s, nil
}

func (s *inspectServer) close() { _ = s.srv.Close() }

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// 本文を画面に出せる文字列にする(gzip は展開、バイナリは件数だけ)
func renderBody(b []byte, encoding string, truncated bool, total int64) map[string]any {
	res := map[string]any{"size": total, "truncated": truncated}
	if len(b) == 0 {
		res["text"] = ""
		return res
	}
	data := b
	if strings.EqualFold(encoding, "gzip") {
		if zr, err := gzip.NewReader(bytes.NewReader(b)); err == nil {
			if d, _ := io.ReadAll(io.LimitReader(zr, 4*maxCapture)); len(d) > 0 {
				data = d
			}
		}
	}
	if !utf8.Valid(data) && !(truncated && utf8.Valid(data[:len(data)-1])) {
		res["binary"] = true
		return res
	}
	res["text"] = string(data)
	return res
}
