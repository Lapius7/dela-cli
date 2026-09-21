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

// fmt.Println等が書く素の"\n"だけだと、環境によって(特にWindowsの一部の端末)
// カーソルが行頭に戻らず、行を追うごとに右へずれていく表示崩れが起きる。
// このプログラム自身が出す文字列は必ずこれを通し、"\r\n"で改行する。
func out(s string) {
	fmt.Print(strings.ReplaceAll(s, "\n", "\r\n"))
}

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
	verbose := false
	args := os.Args[1:]
	filtered := args[:0]
	for _, a := range args {
		if a == "-v" || a == "--verbose" {
			verbose = true
			continue
		}
		filtered = append(filtered, a)
	}
	if len(filtered) < 1 {
		printUsage()
		os.Exit(2)
	}
	target, err := resolveTarget(filtered[0])
	if err != nil {
		fail(err)
	}
	run(target, verbose)
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
	out(fmt.Sprintf(`%s %s

%s
  %s        例: dela 3000        (localhost:3000 を公開)
  %s   例: dela 127.0.0.1:8080
  %s          接続の生ログも表示する

実行するとランダムなサブドメインのURLが発行され、外部からアクセスできるようになります。
Ctrl+C でトンネルを終了します(URLも即座に無効になります)。

%s
  - システムに ssh コマンドが必要です(Windows 10/11・macOS・Linuxに標準搭載)。
  - 接続にはあらかじめ登録した公開鍵が必要です(未登録の場合は接続を拒否されます)。
  - 初回接続時、ホスト鍵の確認が出ます。フィンガープリントが次と一致することを確認してください:
      %s
`,
		bold("dela"), dim("(deplapius) — ローカルのポートを https://xxxx.deploy.lapius7.com として即座に公開する"),
		bold("使い方:"),
		cyan("dela <port>"),
		cyan("dela <host:port>"),
		cyan("-v, --verbose"),
		bold("事前準備:"),
		yellow(hostKeyFingerprint),
	))
}

var (
	urlLineRe = regexp.MustCompile(`https://[a-z0-9]+\.` + regexp.QuoteMeta(tunnelHost))
	// sishは色付け(SGR)だけでなく、カーソル移動・行クリア等のCSIシーケンスも送ってくる。
	// CSI全般([0-9;]*の後に英字1文字で終わるシーケンス)を丸ごと除去する。
	ansiCSIRe = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)
	// sish接続時の定型的な前置きメッセージ。verboseでなければ表示しない(ノイズなので)。
	noisyLineRe = regexp.MustCompile(`^(Press Ctrl-C to close the session\.|The subdomain .* is unavailable\. Assigning a random subdomain\.|Starting SSH Forwarding service for .*)$`)
)

// 端末制御コード(色・カーソル移動・復帰)を取り除き、表示に使える平文だけを残す
func sanitize(s string) string {
	s = ansiCSIRe.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r", "")
	return strings.TrimSpace(s)
}

func run(target string, verbose bool) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		out("\n" + green("✓") + " 終了しました。トンネルは無効になりました。\n")
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

	out(dim("dela · "+tunnelHost) + "\n\n")

	sp := newSpinner("接続中")
	sp.start()

	if err := cmd.Start(); err != nil {
		sp.stop()
		fail(fmt.Errorf("sshの起動に失敗しました(sshコマンドがPATHに無い可能性があります): %w", err))
	}

	printed := false
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := sanitize(scanner.Text())
		if line == "" {
			continue
		}
		if m := urlLineRe.FindString(line); m != "" && !printed {
			sp.stop()
			printBanner(m, target)
			printed = true
			continue
		}
		if !verbose && noisyLineRe.MatchString(line) {
			continue
		}
		if !printed {
			// URL判明前の想定外の行は、接続時の問題を見逃さないようスピナーを止めて出す
			sp.stop()
			out(dim(line) + "\n")
			sp.start()
			continue
		}
		out(dim(line) + "\n")
	}
	sp.stop()

	err = cmd.Wait()
	if ctx.Err() == context.Canceled {
		return
	}
	if err != nil {
		fail(fmt.Errorf("接続が終了しました: %w", err))
	}
}

func printBanner(url, target string) {
	out(fmt.Sprintf(
		"  %s %s\n\n    %s  %s\n        %s %s\n\n  %s\n\n",
		green("✓"), bold("トンネルを確立しました"),
		"🔗", bold(cyan(url)),
		dim("→"), dim(target),
		dim("Ctrl+C で終了"),
	))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, red("エラー:"), err)
	os.Exit(1)
}
