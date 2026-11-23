package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func makeArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	tw.Close()
	zw.Close()
	return buf.Bytes()
}

func TestAssetName(t *testing.T) {
	cases := map[[2]string]string{
		{"linux", "amd64"}:   "dela-linux-x64.tar.gz",
		{"linux", "arm64"}:   "dela-linux-arm64.tar.gz",
		{"darwin", "arm64"}:  "dela-darwin-arm64.tar.gz",
		{"windows", "amd64"}: "dela-win32-x64.tar.gz",
	}
	for in, want := range cases {
		if got := assetName(in[0], in[1]); got != want {
			t.Errorf("%v = %s, want %s", in, got, want)
		}
	}
}

func TestNewerVersion(t *testing.T) {
	cases := []struct {
		cur, latest string
		want        bool
	}{
		{"0.2.0", "v0.3.0", true}, {"0.3.0", "v0.3.0", false}, {"0.10.0", "0.9.0", false},
		{"1.0.0", "v0.99.9", false}, {"0.2.9", "0.2.10", true}, {"0.2.0", "v0.2.1-rc1", true},
	}
	for _, c := range cases {
		got, err := newerVersion(c.cur, c.latest)
		if err != nil || got != c.want {
			t.Errorf("%s→%s: %v %v", c.cur, c.latest, got, err)
		}
	}
	if _, err := newerVersion("dev", "v0.3.0"); err == nil {
		t.Error("読めない版はエラー")
	}
}

func TestChecksum(t *testing.T) {
	sum := sha256.Sum256([]byte("data"))
	h := hex.EncodeToString(sum[:])
	sums := []byte(h + "  dela-linux-x64.tar.gz\n" + strings.Repeat("0", 64) + " *other.tar.gz\n")
	if got, ok := checksumFor(sums, "dela-linux-x64.tar.gz"); !ok || got != h {
		t.Errorf("%s %v", got, ok)
	}
	if got, ok := checksumFor(sums, "other.tar.gz"); !ok || got != strings.Repeat("0", 64) {
		t.Errorf("バイナリ表記(*): %s %v", got, ok)
	}
	if _, ok := checksumFor(sums, "missing.tar.gz"); ok {
		t.Error("無いものは見つからない")
	}
	if err := verifyChecksum([]byte("data"), h); err != nil {
		t.Error(err)
	}
	if err := verifyChecksum([]byte("tampered"), h); err == nil {
		t.Error("不一致はエラー")
	}
}

func TestExtractBinary(t *testing.T) {
	a := makeArchive(t, map[string]string{"./dela": "BIN", "./README": "x"})
	b, err := extractBinary(a, "dela")
	if err != nil || string(b) != "BIN" {
		t.Fatalf("%s %v", b, err)
	}
	if _, err := extractBinary(a, "dela.exe"); err == nil {
		t.Error("無いファイルはエラー")
	}
}

func TestReplaceExecutable(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "dela")
	os.WriteFile(exe, []byte("old"), 0o755)
	if err := replaceExecutable(exe, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "new" {
		t.Errorf("%s", b)
	}
	if _, err := os.Stat(exe + ".new"); err == nil {
		t.Error("一時ファイルが残っている")
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(exe); st.Mode()&0o111 == 0 {
			t.Error("実行権限がない")
		}
	}
}

// releaseServer は GitHub Releases の代わりに最新リリースと資産を返す
func releaseServer(t *testing.T, archive []byte, sums string, withSums bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		name := assetName(runtime.GOOS, runtime.GOARCH)
		assets := fmt.Sprintf(`{"name":%q,"browser_download_url":"%s/a"}`, name, srv.URL)
		if withSums {
			assets += fmt.Sprintf(`,{"name":"checksums.txt","browser_download_url":"%s/sums"}`, srv.URL)
		}
		fmt.Fprintf(w, `{"tag_name":"v9.9.9","assets":[%s]}`, assets)
	})
	mux.HandleFunc("/a", func(w http.ResponseWriter, r *http.Request) { w.Write(archive) })
	mux.HandleFunc("/sums", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, sums) })
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestApplyUpdate(t *testing.T) {
	exeName := "dela"
	if runtime.GOOS == "windows" {
		exeName = "dela.exe"
	}
	archive := makeArchive(t, map[string]string{"./" + exeName: "NEWBIN"})
	sum := sha256.Sum256(archive)
	name := assetName(runtime.GOOS, runtime.GOARCH)
	good := hex.EncodeToString(sum[:]) + "  " + name + "\n"

	run := func(srv *httptest.Server) (string, error) {
		old := releasesAPI
		releasesAPI = srv.URL + "/latest"
		defer func() { releasesAPI = old }()
		exe := filepath.Join(t.TempDir(), exeName)
		os.WriteFile(exe, []byte("OLD"), 0o755)
		rel, err := fetchLatestRelease()
		if err != nil {
			t.Fatal(err)
		}
		err = applyUpdate(rel, exe, runtime.GOOS, runtime.GOARCH)
		b, _ := os.ReadFile(exe)
		return string(b), err
	}

	if got, err := run(releaseServer(t, archive, good, true)); err != nil || got != "NEWBIN" {
		t.Errorf("成功: %s %v", got, err)
	}
	bad := strings.Repeat("0", 64) + "  " + name + "\n"
	if got, err := run(releaseServer(t, archive, bad, true)); err == nil || got != "OLD" {
		t.Errorf("チェックサム不一致では置き換えない: %s %v", got, err)
	}
	if got, err := run(releaseServer(t, archive, good, false)); err == nil || got != "OLD" || !strings.Contains(err.Error(), "チェックサム") {
		t.Errorf("checksums.txt が無いリリースは更新しない: %s %v", got, err)
	}
	if got, err := run(releaseServer(t, archive, "", true)); err == nil || got != "OLD" {
		t.Errorf("checksums.txt に記載が無ければ更新しない: %s %v", got, err)
	}
}

func TestNewerVersionPrerelease(t *testing.T) {
	cases := []struct {
		cur, latest string
		want        bool
	}{
		{"0.3.0-rc1", "v0.3.0", true}, {"0.3.0", "v0.3.0-rc1", false},
		{"0.3.0-rc1", "v0.3.0-rc2", true}, {"0.3.0-rc2", "v0.3.0-rc2", false},
	}
	for _, c := range cases {
		if got, err := newerVersion(c.cur, c.latest); err != nil || got != c.want {
			t.Errorf("%s→%s: %v %v", c.cur, c.latest, got, err)
		}
	}
}
