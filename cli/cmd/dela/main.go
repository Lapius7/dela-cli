// dela (deplapius) は、ローカルのポートを https://xxxx.deploy.lapius7.com として
// 即座に公開するCLI。try.cloudflare.comの`cloudflared tunnel --url`相当を、
// 自前のトンネルサーバー(sish, deploy.lapius7.com)に対してsshクライアントを
// 呼び出す薄いラッパーとして実装している(SSHプロトコル自体は再実装しない)。
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strings"
)

const (
	tunnelHost = "deploy.lapius7.com"
	tunnelPort = "2200"
	// 初回接続時にsshが表示するホスト鍵確認プロンプトで、フィンガープリントが
	// これと一致することを確認してから "yes" と答えること(なりすまし対策)。
	hostKeyFingerprint = "SHA256:TFfR22+f3m4Fpp/5u3svHfC95Srtq+OyhWqylM8ioGc"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "-h", "--help", "help":
		printUsage()
		return
	}
	target, err := resolveTarget(os.Args[1])
	if err != nil {
		fail(err)
	}
	run(target)
}

// "3000" のようなポート番号だけの指定は localhost:3000 として扱う。
// "host:port" 形式ならそのまま使う。
func resolveTarget(arg string) (string, error) {
	if strings.Contains(arg, ":") {
		return arg, nil
	}
	for _, c := range arg {
		if c < '0' || c > '9' {
			return "", fmt.Errorf("ポート番号または host:port の形式で指定してください(例: dela 3000)")
		}
	}
	return "localhost:" + arg, nil
}

func printUsage() {
	fmt.Printf(`%s — ローカルのポートを https://xxxx.deploy.lapius7.com として即座に公開する

使い方:
  dela <port>        例: dela 3000        (localhost:3000 を公開)
  dela <host:port>   例: dela 127.0.0.1:8080

実行するとランダムなサブドメインのURLが発行され、外部からアクセスできるようになります。
Ctrl+C でトンネルを終了します(URLも即座に無効になります)。

事前準備:
  - システムに ssh コマンドが必要です(Windows 10/11・macOS・Linuxに標準搭載)。
  - 接続にはあらかじめ登録した公開鍵が必要です(未登録の場合は接続を拒否されます)。
  - 初回接続時、ホスト鍵の確認が出ます。フィンガープリントが次と一致することを確認してください:
      %s
`, "dela (deplapius)", hostKeyFingerprint)
}

var (
	urlLineRe = regexp.MustCompile(`https://[a-z0-9]+\.` + regexp.QuoteMeta(tunnelHost))
	ansiRe    = regexp.MustCompile(`\x1b\[[0-9;]*m`)
)

func run(target string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		fmt.Println("\n終了しています…")
		cancel()
	}()

	cmd := exec.CommandContext(ctx, "ssh",
		"-p", tunnelPort,
		"-R", "x:80:"+target,
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ServerAliveInterval=30",
		tunnelHost,
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fail(err)
	}
	// ホスト鍵確認・エラー等はそのままユーザーの端末に出す
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Start(); err != nil {
		fail(fmt.Errorf("sshの起動に失敗しました(sshコマンドがPATHに無い可能性があります): %w", err))
	}

	printed := false
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := ansiRe.ReplaceAllString(scanner.Text(), "")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if m := urlLineRe.FindString(line); m != "" && !printed {
			fmt.Printf("\n🔗 %s\n   → %s へ転送中\n   (Ctrl+C で終了)\n\n", m, target)
			printed = true
			continue
		}
		fmt.Println(line)
	}

	err = cmd.Wait()
	if ctx.Err() == context.Canceled {
		return
	}
	if err != nil {
		fail(fmt.Errorf("接続が終了しました: %w", err))
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "エラー:", err)
	os.Exit(1)
}
