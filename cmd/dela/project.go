package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	projectFileName = ".delaconfig"
	defaultLabel    = "app"
	registryBase    = "https://" + tunnelHost + "/_api/v1"
)

// projectID は .delaconfig に保存するこのプロジェクトの識別情報。
// id がサブドメインの一部(<label>-<id>.try.lapius7.si)になり、token がその持ち主であることの証明になる。
type projectID struct {
	ID    string `json:"id"`
	Token string `json:"token"`
	Host  string `json:"host,omitempty"`
}

var labelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,18}[a-z0-9])?$`)

func validateLabel(l string) error {
	if !labelRe.MatchString(l) {
		return fmt.Errorf("--name は英小文字・数字・ハイフンで 1〜20 文字にしてください(例: api, web-2)")
	}
	return nil
}

// カレントディレクトリから親に向かって .delaconfig を探す
func findProjectFile() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	home, _ := os.UserHomeDir()
	for {
		p := filepath.Join(dir, projectFileName)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, true
		}
		parent := filepath.Dir(dir)
		if parent == dir || dir == home {
			return "", false
		}
		dir = parent
	}
}

// 新規作成の場所: git リポジトリ内ならそのルート、そうでなければカレントディレクトリ
func newProjectDir() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return cwd
		}
		dir = parent
	}
}

func readProjectFile(path string) (projectID, error) {
	var p projectID
	b, err := os.ReadFile(path)
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(b, &p); err != nil || p.ID == "" || p.Token == "" {
		return p, fmt.Errorf("%s の形式が正しくありません(削除して dela を実行し直すと再発行されます)", path)
	}
	return p, nil
}

func writeProjectFile(path string, p projectID) error {
	p.Host = tunnelHost
	b, _ := json.MarshalIndent(p, "", "  ")
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

var httpClient = &http.Client{Timeout: 15 * time.Second}

func registryPost(path string, body any) (*http.Response, []byte, error) {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req, err := http.NewRequest("POST", registryBase+path, &buf)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "dela/"+cliVersion)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return resp, b, nil
}

// サーバーに新しい ID を発行してもらう
func registerNewID() (projectID, error) {
	var p projectID
	resp, b, err := registryPost("/register", nil)
	if err != nil {
		return p, fmt.Errorf("サーバーに接続できませんでした: %w", err)
	}
	switch resp.StatusCode {
	case http.StatusCreated:
		if err := json.Unmarshal(b, &p); err != nil || p.ID == "" || p.Token == "" {
			return p, errors.New("サーバーの応答が不正です")
		}
		return p, nil
	case http.StatusTooManyRequests:
		return p, errors.New("発行の回数が上限に達しました。しばらくしてからやり直してください")
	default:
		return p, fmt.Errorf("ID の発行に失敗しました(HTTP %d)", resp.StatusCode)
	}
}

// 古い ID をサーバーから解放する(失敗しても致命的ではない)
func releaseID(p projectID) {
	_, _, _ = registryPost("/release", map[string]string{"id": p.ID, "token": p.Token})
}

// .gitignore に .delaconfig が無ければ追記する(token をうっかりコミットしないため)。追記したら true
func ensureGitignored(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return false
	}
	gi := filepath.Join(dir, ".gitignore")
	b, _ := os.ReadFile(gi)
	for _, l := range strings.Split(string(b), "\n") {
		if t := strings.TrimSpace(l); t == projectFileName || t == "/"+projectFileName {
			return false
		}
	}
	f, err := os.OpenFile(gi, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return false
	}
	defer f.Close()
	if len(b) > 0 && !strings.HasSuffix(string(b), "\n") {
		_, _ = f.WriteString("\n")
	}
	_, err = f.WriteString(projectFileName + "\n")
	return err == nil
}

// このプロジェクトの ID を返す。無ければサーバーで発行して .delaconfig に保存する。
func loadOrCreateProject(temp bool) (p projectID, path string, created bool, ignored bool, err error) {
	if temp {
		// 一時モード: 設定ファイルを読まず・作らず、使い捨ての ID を発行する
		p, err = registerNewID()
		return p, "", true, false, err
	}
	if path, ok := findProjectFile(); ok {
		p, err = readProjectFile(path)
		return p, path, false, false, err
	}
	p, err = registerNewID()
	if err != nil {
		return
	}
	dir := newProjectDir()
	path = filepath.Join(dir, projectFileName)
	if err = writeProjectFile(path, p); err != nil {
		err = fmt.Errorf("%s を保存できませんでした: %w", path, err)
		return
	}
	return p, path, true, ensureGitignored(dir), nil
}

func projectURL(label, id string) string {
	return "https://" + label + "-" + id + "." + tunnelHost
}

// dela renew: このプロジェクトのサブドメインを作り直す(古い URL は無効になる)
func cmdRenew() {
	path, ok := findProjectFile()
	if !ok {
		out(dim("このプロジェクトにはまだ .delaconfig がありません。dela <port> を実行すると自動で作られます。") + "\n")
		return
	}
	old, oldErr := readProjectFile(path)
	np, err := registerNewID()
	if err != nil {
		fail(err)
	}
	if err := writeProjectFile(path, np); err != nil {
		fail(fmt.Errorf("%s を保存できませんでした: %w", path, err))
	}
	if oldErr == nil {
		releaseID(old)
	}
	out(fmt.Sprintf("%s サブドメインを再発行しました\n    %s\n    %s\n", green("✓"), bold(cyan(projectURL(defaultLabel, np.ID))), dim("(古い URL は無効です)")))
}

// dela info: このプロジェクトに割り当てられている URL を表示する
func cmdInfo() {
	path, ok := findProjectFile()
	if !ok {
		out(dim("このプロジェクトにはまだ .delaconfig がありません。dela <port> を実行すると自動で作られます。") + "\n")
		return
	}
	p, err := readProjectFile(path)
	if err != nil {
		fail(err)
	}
	out(fmt.Sprintf("%s\n    %s\n    %s\n", dim(path), bold(cyan(projectURL(defaultLabel, p.ID))), dim("別のサービスは dela <port> --name <名前> で <名前>-"+p.ID+" になります")))
}

// ssh から SSH_ASKPASS として呼ばれたときは、パスワード(= token)だけを出力して終わる
func maybeAskpass() bool {
	if os.Getenv("DELA_ASKPASS") == "1" {
		fmt.Println(os.Getenv("DELA_ASKPASS_TOKEN"))
		return true
	}
	return false
}
