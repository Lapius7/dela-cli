package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// 実行中のトンネルを他のターミナルからも見えるようにするための、ローカルの
// 状態ファイル置き場(~/.dela/state/<pid>.json)。デーモンは持たず、
// dela <port> の各プロセス自身がここに自分の情報を書き込み・消去する。
type tunnelRecord struct {
	PID       int       `json:"pid"`
	URL       string    `json:"url"`
	Target    string    `json:"target"`
	StartedAt time.Time `json:"started_at"`
	// 以下は v0.5.0 で追加(古い記録には無いので omitempty)。dela list --json / info --json で使う
	Name string `json:"name,omitempty"` // --name の値(既定は app)
	Dir  string `json:"dir,omitempty"`  // .delaconfig のあるフォルダ。--temp のときは空
	Temp bool   `json:"temp,omitempty"`
	// 以下は TCP 転送・replica で追加(古い記録には無い)
	Kind    string `json:"kind,omitempty"`    // "tcp" のとき TCP 転送。空は HTTP
	ID      string `json:"id,omitempty"`      // このトンネルのプロジェクト ID(同名の重複起動の判定用。token は入れない)
	Replica bool   `json:"replica,omitempty"` // --replica で起動した
}

func stateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".dela", "state")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func stateFile(pid int) (string, error) {
	dir, err := stateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fmt.Sprintf("%d.json", pid)), nil
}

// registerTunnel/unregisterTunnelはベストエフォート(失敗してもトンネル自体の
// 動作には影響させない。list/stopが多少不正確になるだけ)。
func registerTunnel(rec tunnelRecord) {
	path, err := stateFile(os.Getpid())
	if err != nil {
		return
	}
	rec.PID = os.Getpid()
	rec.StartedAt = time.Now()
	data, err := json.Marshal(rec)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}

func unregisterTunnel() {
	path, err := stateFile(os.Getpid())
	if err != nil {
		return
	}
	_ = os.Remove(path)
}

// listTunnels は生きているプロセスの記録だけを返す。既にプロセスが終了しているのに
// 記録ファイルが残っている場合(強制終了等でdeferが効かなかった場合)は、
// ここで見つけ次第削除する。
func listTunnels() ([]tunnelRecord, error) {
	dir, err := stateDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []tunnelRecord
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var rec tunnelRecord
		if err := json.Unmarshal(data, &rec); err != nil {
			_ = os.Remove(path)
			continue
		}
		if !processAlive(rec.PID) {
			_ = os.Remove(path)
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}

func removeStateFileFor(pid int) {
	path, err := stateFile(pid)
	if err != nil {
		return
	}
	_ = os.Remove(path)
}

// findDuplicate は同じプロジェクト ID・同じ名前・同じ種類(HTTP/TCP)で、既に起動中のトンネルを探す。
// ID を持たない古い記録は判定に使わない。
func findDuplicate(recs []tunnelRecord, id, name, kind string, replica bool) (tunnelRecord, bool) {
	for _, r := range recs {
		if r.ID == "" || r.ID != id || r.Name != name || r.Kind != kind || r.PID == os.Getpid() {
			continue
		}
		// 新しい起動も既存も --replica なら共存できる。どちらかが欠けていれば衝突
		if replica && r.Replica {
			continue
		}
		return r, true
	}
	return tunnelRecord{}, false
}
