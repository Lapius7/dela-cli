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
func registerTunnel(url, target string) {
	path, err := stateFile(os.Getpid())
	if err != nil {
		return
	}
	rec := tunnelRecord{PID: os.Getpid(), URL: url, Target: target, StartedAt: time.Now()}
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
