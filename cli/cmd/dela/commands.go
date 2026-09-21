package main

import (
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"
	"time"
)

const cliVersion = "0.2.0"

// dela list: このPCから起動中のトンネル一覧を表示する
func cmdList() {
	recs, err := listTunnels()
	if err != nil {
		fail(fmt.Errorf("一覧の取得に失敗しました: %w", err))
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
