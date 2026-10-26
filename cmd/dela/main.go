// dela (deplapius) は、ローカルのポートを https://xxxx.try.lapius7.si として
// 即座に公開するCLI。try.cloudflare.comの`cloudflared tunnel --url`相当を、
// 自前のトンネルサーバー(sish, try.lapius7.si)に対してsshクライアントを
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
	"strconv"
	"strings"
	"time"
)

// options は dela <port> のコマンドラインオプション
type options struct {
	inspect     bool   // -i/--inspect: リクエストを記録して表示する
	inspectPort int    // --inspect-port
	auth        string // --auth user:pass
	noReconnect bool   // --no-reconnect
}

const (
	tunnelHost = "try.lapius7.si"
	tunnelPort = "2200"
	// サーバーのED25519ホスト鍵。この鍵をバイナリに固定し、一時known_hostsで
	// 厳格検証する(初回確認プロンプトが不要で、なりすましも防げる)。
	hostKeyFingerprint = "SHA256:TFfR22+f3m4Fpp/5u3svHfC95Srtq+OyhWqylM8ioGc"
	hostPublicKey      = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIL2oBb6EnATO6A1cYCnEBcC5nUNl2/TuCcg/7YxYuvU3"
)

// fmt.Println等が書く素の"\n"だけだと、環境によって(特にWindowsの一部の端末)
// カーソルが行頭に戻らず、行を追うごとに右へずれていく表示崩れが起きる。
// このプログラム自身が出す文字列は必ずこれを通し、"\r\n"で改行する。
func out(s string) {
	fmt.Print(strings.ReplaceAll(s, "\n", "\r\n"))
}

func main() {
	if maybeAskpass() {
		return
	}
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "-h", "--help", "help":
		printUsage()
		return
	case "list":
		cmdList()
		return
	case "stop":
		cmdStop(os.Args[2:])
		return
	case "renew":
		cmdRenew()
		return
	case "info":
		cmdInfo()
		return
	case "version", "-V":
		cmdVersion()
		return
	}

	verbose := false
	label := defaultLabel
	temp := false
	opts := options{auth: os.Getenv("DELA_AUTH")}
	args := os.Args[1:]
	filtered := args[:0:0]
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-v" || a == "--verbose":
			verbose = true
		case a == "-t" || a == "--temp":
			temp = true
		case a == "-i" || a == "--inspect":
			opts.inspect = true
		case a == "--no-reconnect":
			opts.noReconnect = true
		case a == "--inspect-port" || a == "--auth":
			if i+1 >= len(args) {
				fail(fmt.Errorf("%s には値を指定してください", a))
			}
			i++
			setValueOption(&opts, a, args[i])
		case strings.HasPrefix(a, "--inspect-port=") || strings.HasPrefix(a, "--auth="):
			kv := strings.SplitN(a, "=", 2)
			setValueOption(&opts, kv[0], kv[1])
		case a == "-n" || a == "--name":
			if i+1 >= len(args) {
				fail(fmt.Errorf("--name には名前を指定してください(例: --name api)"))
			}
			i++
			label = args[i]
		case strings.HasPrefix(a, "--name="):
			label = strings.TrimPrefix(a, "--name=")
		default:
			filtered = append(filtered, a)
		}
	}
	if err := validateLabel(label); err != nil {
		fail(err)
	}
	if len(filtered) < 1 {
		printUsage()
		os.Exit(2)
	}
	target, err := resolveTarget(filtered[0])
	if err != nil {
		fail(err)
	}
	if opts.auth != "" {
		if u, pw, ok := strings.Cut(opts.auth, ":"); !ok || u == "" || pw == "" {
			fail(fmt.Errorf("--auth は ユーザー名:パスワード の形式で指定してください(例: --auth demo:s3cret)"))
		}
	}
	run(target, label, verbose, temp, opts)
}

func setValueOption(o *options, name, val string) {
	switch name {
	case "--auth":
		o.auth = val
	case "--inspect-port":
		n, err := strconv.Atoi(val)
		if err != nil || n < 1 || n > 65535 {
			fail(fmt.Errorf("--inspect-port には 1〜65535 の数値を指定してください"))
		}
		o.inspect = true
		o.inspectPort = n
	}
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
  %s   同じプロジェクト内で別のサービスを公開する(例: --name api)
  %s          一時モード(.delaconfig を作らず、終了で URL を破棄する)
  %s          リクエストを記録して表示する(ブラウザで http://127.0.0.1:4040 も開ける)
  %s   公開 URL に Basic 認証をかける(環境変数 DELA_AUTH でも指定可)
  %s   切断されても自動で再接続しない
  %s          接続の生ログも表示する

%s
  %s              このPCから起動中のトンネル一覧を表示する
  %s     PIDを指定してトンネルを停止する
  %s          すべてのトンネルを停止する
  %s              このプロジェクトの URL を表示する
  %s             サブドメインを作り直す(古い URL は無効になる)
  %s           バージョンを表示する

初回の実行で、プロジェクトのフォルダ(git ならルート)に .delaconfig ができ、サーバーが発行した
サブドメインが保存されます。以降は同じプロジェクトなら毎回同じ URL(app-xxxx.try.lapius7.si)です。
.delaconfig には秘密の token が入っています(git の場合は .gitignore に自動で追記します)。
Ctrl+C でトンネルを終了します(URLは次の起動まで無効になります)。

%s
  - システムに ssh コマンドが必要です(Windows 10/11・macOS・Linuxに標準搭載)。
  - 登録や SSH 鍵は不要です(初回に自動でサブドメインが発行されます)。ssh は 8.4 以降が必要です。
  - ホスト鍵は dela に固定されています。フィンガープリントは次のとおりです:
      %s
`,
		bold("dela"), dim("(deplapius) — ローカルのポートを https://xxxx.try.lapius7.si として即座に公開する"),
		bold("使い方:"),
		cyan("dela <port>"),
		cyan("dela <host:port>"),
		cyan("-n, --name <名前>"),
		cyan("-t, --temp"),
		cyan("-i, --inspect"),
		cyan("--auth <user:pass>"),
		cyan("--no-reconnect"),
		cyan("-v, --verbose"),
		bold("管理コマンド:"),
		cyan("dela list"),
		cyan("dela stop <PID>"),
		cyan("dela stop --all"),
		cyan("dela info"),
		cyan("dela renew"),
		cyan("dela version"),
		bold("事前準備:"),
		yellow(hostKeyFingerprint),
	))
}

var (
	urlLineRe = regexp.MustCompile(`https://[a-z0-9-]+\.` + regexp.QuoteMeta(tunnelHost))
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

// 固定したホスト鍵だけを書いた一時known_hostsを作る
func writeKnownHosts() (string, error) {
	f, err := os.CreateTemp("", "dela-known-hosts-*")
	if err != nil {
		return "", err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "[%s]:%s %s\n", tunnelHost, tunnelPort, hostPublicKey)
	return f.Name(), err
}

func run(target, label string, verbose, temp bool, opts options) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		// ここでは表示を行わない。メッセージはrun()側で、実際にssh側の後始末
		// (プロセス終了・パイプクローズ)が終わったあとにまとめて1回だけ出す
		// (ここで即座に出すと、メインループ側の出力と混ざって順序が乱れるため)。
		cancel()
	}()

	proj, projPath, created, ignored, err := loadOrCreateProject(temp)
	if err != nil {
		fail(err)
	}
	if temp {
		defer releaseID(proj)
		out(fmt.Sprintf("  %s %s\n\n", yellow("!"), "一時モード: .delaconfig は使わず、終了すると URL は破棄されます。"))
	} else if created {
		out(fmt.Sprintf("  %s %s %s\n", green("✓"), "このプロジェクト用のサブドメインを発行しました:", dim(projPath)))
		if ignored {
			out(dim("    (秘密の token を含むので .gitignore に .delaconfig を追記しました)") + "\n")
		}
		out("\n")
	}

	knownHosts, err := writeKnownHosts()
	if err != nil {
		fail(err)
	}
	defer os.Remove(knownHosts)

	// 認証・inspect を使うときは、間にローカルのプロキシを挟む(再接続しても同じものを使い続ける)
	forward := target
	var extra []string
	if opts.auth != "" || opts.inspect {
		px, err := startProxy(target, opts)
		if err != nil {
			fail(fmt.Errorf("ローカルプロキシを起動できませんでした: %w", err))
		}
		defer px.close()
		forward = px.addr
		if opts.auth != "" {
			user, _, _ := strings.Cut(opts.auth, ":")
			extra = append(extra, fmt.Sprintf("🔒  Basic 認証が必要です(ユーザー名: %s)", user))
		}
		if opts.inspect {
			is, err := startInspect(px.store, opts.inspectPort)
			if err != nil {
				fail(fmt.Errorf("inspect 画面を起動できませんでした: %w", err))
			}
			defer is.close()
			extra = append(extra, "🔍  inspect: http://"+is.Addr)
		}
	}

	out(dim("dela · "+tunnelHost) + "\n\n")

	everConnected := false
	failures := 0
	backoff := time.Second
	for {
		connected, err := session(ctx, sessionArgs{
			proj: proj, label: label, target: target, forward: forward, knownHosts: knownHosts,
			verbose: verbose, first: !everConnected, extra: extra,
		})
		if ctx.Err() != nil {
			if everConnected {
				unregisterTunnel()
			}
			printShutdown(everConnected)
			return
		}
		if connected {
			everConnected = true
			failures = 0
			backoff = time.Second
		} else {
			failures++
		}
		if !everConnected || opts.noReconnect || failures > 30 {
			if everConnected {
				unregisterTunnel()
			}
			if !everConnected {
				out(dim("  同じ名前のトンネルが既に起動中なら dela list / dela stop で止めてください。認証に失敗する場合は dela renew でサブドメインを再発行してください。") + "\n")
			}
			if err == nil {
				err = fmt.Errorf("接続できませんでした")
			}
			fail(fmt.Errorf("接続が終了しました: %w", err))
		}
		out(fmt.Sprintf("\n  %s 接続が切れました。%d秒後に再接続します…(終了は Ctrl+C)\n", yellow("!"), int(backoff.Seconds())))
		select {
		case <-ctx.Done():
			unregisterTunnel()
			printShutdown(true)
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

type sessionArgs struct {
	proj                   projectID
	label, target, forward string
	knownHosts             string
	verbose, first         bool
	extra                  []string
}

// session は ssh を1回起動し、切れるまで待つ。URL が確立したら connected=true を返す。
func session(ctx context.Context, a sessionArgs) (connected bool, err error) {
	cmd := exec.CommandContext(ctx, "ssh",
		"-p", tunnelPort,
		"-l", a.proj.ID,
		"-o", "PreferredAuthentications=password",
		"-o", "NumberOfPasswordPrompts=1",
		"-o", "UserKnownHostsFile="+a.knownHosts,
		"-o", "GlobalKnownHostsFile="+os.DevNull,
		"-o", "StrictHostKeyChecking=yes",
		"-R", a.label+":80:"+a.forward,
		"-o", "ExitOnForwardFailure=yes",
		// 鍵なしの公開サーバー: 手元の鍵・エージェントは使わない(誤った鍵で切断されるのを防ぐ)
		"-o", "PubkeyAuthentication=no",
		"-o", "IdentitiesOnly=yes",
		"-o", "IdentityAgent=none",
		// 回線が死んだら早めに気づいて再接続できるようにする
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"-o", "ConnectTimeout=15",
		tunnelHost,
	)
	exe, _ := os.Executable()
	cmd.Env = append(os.Environ(),
		"SSH_ASKPASS="+exe,
		"SSH_ASKPASS_REQUIRE=force",
		"DISPLAY=:0",
		"DELA_ASKPASS=1",
		"DELA_ASKPASS_TOKEN="+a.proj.Token,
	)
	stdout, perr := cmd.StdoutPipe()
	if perr != nil {
		fail(perr)
	}
	// ホスト鍵確認・エラー等はそのままユーザーの端末に出す
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	sp := newSpinner("接続中")
	if a.first {
		sp.start()
	}

	if serr := cmd.Start(); serr != nil {
		sp.stop()
		fail(fmt.Errorf("sshの起動に失敗しました(sshコマンドがPATHに無い可能性があります): %w", serr))
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
			if a.first {
				printBanner(m, a.target, a.extra)
				registerTunnel(m, a.target)
			} else {
				out(fmt.Sprintf("  %s 再接続しました: %s\n", green("✓"), cyan(m)))
			}
			printed = true
			continue
		}
		if !a.verbose && noisyLineRe.MatchString(line) {
			continue
		}
		if !printed {
			// URL判明前の想定外の行は、接続時の問題を見逃さないようスピナーを止めて出す
			sp.stop()
			out(dim(line) + "\n")
			if a.first {
				sp.start()
			}
			continue
		}
		out(dim(line) + "\n")
	}
	sp.stop()
	err = cmd.Wait()
	return printed, err
}

func printBanner(url, target string, extra []string) {
	out(fmt.Sprintf(
		"  %s %s\n\n    %s  %s\n        %s %s\n\n  %s\n\n",
		green("✓"), bold("トンネルを確立しました"),
		"🔗", bold(cyan(url)),
		dim("→"), dim(target),
		dim("Ctrl+C で終了 · 他のターミナルからは dela list / dela stop で確認・停止できます"),
	))
	if len(extra) > 0 {
		// バナーの空行の手前に入れず、続けて表示する
		for _, e := range extra {
			out("    " + e + "\n")
		}
		out("\n")
	}
}

// Ctrl+C(SIGINT)で終了した際の確定メッセージ。トンネルが確立済みだったかどうかで文言を変える。
func printShutdown(hadTunnel bool) {
	if hadTunnel {
		out(fmt.Sprintf("  %s %s\n\n", green("✓"), "終了しました。トンネルは無効になりました。"))
		return
	}
	out(fmt.Sprintf("  %s %s\n\n", yellow("!"), "接続を中止しました。"))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, red("エラー:"), err)
	os.Exit(1)
}
