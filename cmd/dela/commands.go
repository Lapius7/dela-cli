package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// cliVersion は npm 版では -ldflags "-X main.cliVersion=..." で埋め込む
var cliVersion = "0.2.0"

// hasFlag は args に flag が含まれるかを返す
func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// printJSON は v を 1 行の JSON で標準出力へ書く(他のツールから読む用。色や案内文は混ぜない)
func printJSON(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		fail(err)
	}
	fmt.Println(string(b))
}

// tunnelJSON は dela list --json の 1 件分
type tunnelJSON struct {
	PID       int       `json:"pid"`
	URL       string    `json:"url"`
	Name      string    `json:"name"`
	Target    string    `json:"target"`
	Dir       string    `json:"dir"`
	Temp      bool      `json:"temp"`
	StartedAt time.Time `json:"started_at"`
}

func toTunnelJSON(r tunnelRecord) tunnelJSON {
	name := r.Name
	if name == "" {
		name = nameFromURL(r.URL)
	}
	return tunnelJSON{PID: r.PID, URL: r.URL, Name: name, Target: r.Target, Dir: r.Dir, Temp: r.Temp, StartedAt: r.StartedAt}
}

// nameFromURL は https://<name>-<id>.host の <name> を返す(古い記録に name が無いとき用)
func nameFromURL(u string) string {
	host := strings.TrimPrefix(u, "https://")
	host, _, _ = strings.Cut(host, ".")
	if i := strings.LastIndex(host, "-"); i > 0 {
		return host[:i]
	}
	return host
}

// dela list: このPCから起動中のトンネル一覧を表示する(--json で機械向けの JSON 配列)
func cmdList(args []string) {
	recs, err := listTunnels()
	if err != nil {
		fail(fmt.Errorf("一覧の取得に失敗しました: %w", err))
	}
	if hasFlag(args, "--json") {
		list := make([]tunnelJSON, 0, len(recs))
		for _, r := range recs {
			list = append(list, toTunnelJSON(r))
		}
		printJSON(list)
		return
	}
	if len(recs) == 0 {
		out(dim("実行中のトンネルはありません。") + "\n")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintf(w, "%s\t%s\t%s\t%s\r\n", bold("PID"), bold("URL"), bold("→ 転送先"), bold("経過"))
	for _, r := range recs {
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\r\n", r.PID, cyan(r.URL), r.Target, dim(humanDuration(time.Since(r.StartedAt))))
	}
	w.Flush()
	out("\n" + dim("停止するには: dela stop <PID> または dela stop --all") + "\n")
}

// dela stop <PID> / dela stop --all: 起動中のトンネルを停止する
func cmdStop(args []string) {
	if len(args) == 0 {
		fail(fmt.Errorf("PIDまたは --all を指定してください(例: dela stop 12345 / dela stop --all)"))
	}
	recs, err := listTunnels()
	if err != nil {
		fail(fmt.Errorf("一覧の取得に失敗しました: %w", err))
	}
	if len(recs) == 0 {
		out(dim("実行中のトンネルはありません。") + "\n")
		return
	}

	var targets []tunnelRecord
	if args[0] == "--all" || args[0] == "-a" {
		targets = recs
	} else {
		pid, err := strconv.Atoi(args[0])
		if err != nil {
			fail(fmt.Errorf("PIDは数値で指定してください(例: dela stop 12345)"))
		}
		for _, r := range recs {
			if r.PID == pid {
				targets = append(targets, r)
			}
		}
		if len(targets) == 0 {
			fail(fmt.Errorf("PID %d のトンネルは見つかりませんでした(dela list で確認してください)", pid))
		}
	}

	for _, r := range targets {
		proc, err := os.FindProcess(r.PID)
		if err == nil {
			_ = proc.Kill()
		}
		// killが失敗しても、既に死んでいる可能性が高いのでどちらにせよ記録は消す
		removeStateFileFor(r.PID)
		out(fmt.Sprintf("%s PID %d (%s) を停止しました\n", green("✓"), r.PID, r.URL))
	}
}

func cmdVersion() {
	out(fmt.Sprintf("dela version %s\n", cliVersion))
}

// 経過時間を「3分」「1時間20分」のような日本語表記にする
func humanDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%d秒", int(d.Seconds()))
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h == 0 {
		return fmt.Sprintf("%d分", m)
	}
	return fmt.Sprintf("%d時間%d分", h, m)
}
