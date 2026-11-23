package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// dela update: GitHub Releases から最新版を取得して、実行中の dela 自身を置き換える。
// リリースに添付された checksums.txt の SHA-256 と一致しなければ置き換えない。

const maxDownload = 100 << 20 // 100MB

// releasesAPI は最新リリースの取得先(テストで差し替える)
var releasesAPI = "https://api.github.com/repos/Lapius7/dela-cli/releases/latest"

var updateClient = &http.Client{Timeout: 2 * time.Minute}

type releaseInfo struct {
	Tag    string         `json:"tag_name"`
	Assets []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

func (r releaseInfo) asset(name string) (releaseAsset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return releaseAsset{}, false
}

// assetName はリリースに添付されるアーカイブ名(npm の os-cpu 名に合わせた dela-<os>-<cpu>.tar.gz)
func assetName(goos, goarch string) string {
	osName := goos
	if osName == "windows" {
		osName = "win32"
	}
	cpu := goarch
	if cpu == "amd64" {
		cpu = "x64"
	}
	return "dela-" + osName + "-" + cpu + ".tar.gz"
}

// parseVersion は v1.2.3 / 1.2.3 / 1.2.3-rc1 の数値部分 [major minor patch] を返す
func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	v, _, _ = strings.Cut(v, "-")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func prereleaseOf(v string) string {
	_, pre, _ := strings.Cut(strings.TrimPrefix(strings.TrimSpace(v), "v"), "-")
	return pre
}

func hasPrerelease(v string) bool { return prereleaseOf(v) != "" }

// newerVersion は latest が current より新しいかを返す。どちらかが読めないときはエラー。
func newerVersion(current, latest string) (bool, error) {
	c, ok1 := parseVersion(current)
	l, ok2 := parseVersion(latest)
	if !ok1 || !ok2 {
		return false, fmt.Errorf("バージョンを比較できません(現在 %q / 最新 %q)", current, latest)
	}
	for i := range c {
		if l[i] != c[i] {
			return l[i] > c[i], nil
		}
	}
	// 数値が同じなら、正式版はプレリリース(-rc1 など)より新しい
	cp, lp := hasPrerelease(current), hasPrerelease(latest)
	if cp && !lp {
		return true, nil
	}
	if cp && lp {
		return prereleaseOf(latest) > prereleaseOf(current), nil
	}
	return false, nil
}

func httpGet(url string, limit int64) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "dela/"+cliVersion)
	resp, err := updateClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s に接続できませんでした(HTTP %d)", url, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("ダウンロードが大きすぎます")
	}
	return b, nil
}

func fetchLatestRelease() (releaseInfo, error) {
	var r releaseInfo
	b, err := httpGet(releasesAPI, 1<<20)
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(b, &r); err != nil || r.Tag == "" {
		return r, errors.New("リリース情報を読み取れませんでした")
	}
	return r, nil
}

// checksumFor は checksums.txt(sha256sum の出力形式)から name の SHA-256 を探す
func checksumFor(sums []byte, name string) (string, bool) {
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name && len(f[0]) == 64 {
			return strings.ToLower(f[0]), true
		}
	}
	return "", false
}

func verifyChecksum(data []byte, want string) error {
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("チェックサムが一致しません(期待 %s / 実際 %s)。更新を中止しました", want, got)
	}
	return nil
}

// extractBinary は tar.gz から dela(Windows は dela.exe)を取り出す。エントリは ./dela のように ./ が付く。
func extractBinary(archive []byte, exeName string) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("アーカイブに %s が含まれていません", exeName)
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && path.Base(h.Name) == exeName {
			b, err := io.ReadAll(io.LimitReader(tr, maxDownload+1))
			if err != nil {
				return nil, err
			}
			if int64(len(b)) > maxDownload {
				return nil, errors.New("バイナリが大きすぎます")
			}
			return b, nil
		}
	}
}

// replaceExecutable は exePath を data で置き換える。Windows は実行中の exe を上書きできないので、
// 実行中のものを .old に退避してから新しいものを置く(.old は次回の更新で消す)。
func replaceExecutable(exePath string, data []byte) error {
	// 同時に2つの更新が走っても壊れないよう、実行ファイルと同じ場所に固有の一時ファイルを作る
	f, err := os.CreateTemp(filepath.Dir(exePath), ".dela-new-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp, 0o755)
	}
	if werr != nil {
		_ = os.Remove(tmp)
		return werr
	}
	if runtime.GOOS != "windows" {
		if err := os.Rename(tmp, exePath); err != nil {
			_ = os.Remove(tmp)
			return err
		}
		return nil
	}
	old := exePath + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exePath, old); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, exePath); err != nil {
		_ = os.Rename(old, exePath) // 元に戻す
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func cmdUpdate(args []string) {
	checkOnly := hasFlag(args, "--check")
	exe, err := os.Executable()
	if err != nil {
		fail(err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if strings.Contains(filepath.ToSlash(exe), "/node_modules/") {
		fail(errors.New("pnpm で入れた dela は自己更新しません。pnpm add -g @lapius/dela-cli で更新してください"))
	}
	rel, err := fetchLatestRelease()
	if err != nil {
		fail(fmt.Errorf("最新版を確認できませんでした: %w", err))
	}
	newer, err := newerVersion(cliVersion, rel.Tag)
	if err != nil {
		fail(err)
	}
	if !newer {
		out(fmt.Sprintf("%s すでに最新です(%s)\n", green("✓"), cliVersion))
		return
	}
	if checkOnly {
		out(fmt.Sprintf("新しい版があります: %s → %s(dela update で更新)\n", cliVersion, strings.TrimPrefix(rel.Tag, "v")))
		return
	}
	if err := applyUpdate(rel, exe, runtime.GOOS, runtime.GOARCH); err != nil {
		fail(err)
	}
	out(fmt.Sprintf("%s %s → %s に更新しました\n", green("✓"), cliVersion, strings.TrimPrefix(rel.Tag, "v")))
	out(dim("    サービスで動かしている場合は、次の起動から新しい版になります(今すぐなら dela service install をやり直してください)") + "\n")
}

// applyUpdate はアーカイブとチェックサムを取得し、検証できたときだけ exe を置き換える
func applyUpdate(rel releaseInfo, exe, goos, goarch string) error {
	name := assetName(goos, goarch)
	asset, ok := rel.asset(name)
	if !ok {
		return fmt.Errorf("このリリース(%s)に %s がありません", rel.Tag, name)
	}
	sumAsset, ok := rel.asset("checksums.txt")
	if !ok {
		return fmt.Errorf("このリリース(%s)にはチェックサム(checksums.txt)が無いため、更新しません", rel.Tag)
	}
	sums, err := httpGet(sumAsset.URL, 1<<20)
	if err != nil {
		return fmt.Errorf("チェックサムを取得できませんでした: %w", err)
	}
	want, ok := checksumFor(sums, name)
	if !ok {
		return fmt.Errorf("checksums.txt に %s の記載がありません", name)
	}
	archive, err := httpGet(asset.URL, maxDownload)
	if err != nil {
		return fmt.Errorf("ダウンロードに失敗しました: %w", err)
	}
	if err := verifyChecksum(archive, want); err != nil {
		return err
	}
	exeName := "dela"
	if goos == "windows" {
		exeName = "dela.exe"
	}
	bin, err := extractBinary(archive, exeName)
	if err != nil {
		return err
	}
	if err := replaceExecutable(exe, bin); err != nil {
		if os.IsPermission(err) {
			return fmt.Errorf("%s を書き換えられません(権限が足りません): %w", exe, err)
		}
		return fmt.Errorf("置き換えに失敗しました: %w", err)
	}
	return nil
}
