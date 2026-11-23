// dela (deplapius) は、ローカルのポートを https://xxxx.try.lapius7.si として
// 即座に公開するCLI。try.cloudflare.comの`cloudflared tunnel --url`相当を、
// トンネルサーバー(try.lapius7.si)に対してsshクライアントを
// 呼び出す薄いラッパーとして実装している(SSHプロトコル自体は再実装しない)。
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// options は dela <port> のコマンドラインオプション
type options struct {
	inspect     bool   // -i/--inspect: リクエストを記録して表示する
	inspectPort int    // --inspect-port
	auth        string // --auth user:pass
	noReconnect bool   // --no-reconnect
	tcp         bool   // dela tcp <port>
	replica     bool   // --replica: 同じ名前の複数プロセスをサーバー側で振り分ける
	metricsPort int    // --metrics-port: Prometheus 形式の /metrics を 127.0.0.1 で公開する
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
	extractConfigFlag()
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
		cmdList(os.Args[2:])
		return
	case "stop":
		cmdStop(os.Args[2:])
		return
	case "renew":
		cmdRenew()
		return
	case "info":
		cmdInfo(os.Args[2:])
		return
	case "version", "-V":
		cmdVersion()
		return
	case "service":
		cmdService(os.Args[2:])
		return
	case "update":
		cmdUpdate(os.Args[2:])
		return
	}

	tcp := os.Args[1] == "tcp"
	args := os.Args[1:]
	if tcp {
		args = args[1:]
	}
	cfg, err := parseRunArgs(args, tcp)
	if err != nil {
		if errors.Is(err, errUsage) {
			printUsage()
			os.Exit(2)
		}
		fail(err)
	}
	run(cfg.target, cfg.label, cfg.verbose, cfg.temp, cfg.opts)
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
  %s     SSH・RDP など任意の TCP を公開する(例: dela tcp 22 → tcp://try.lapius7.si:<port>)
  %s   同じプロジェクト内で別のサービスを公開する(例: --name api)
  %s          一時モード(.delaconfig を作らず、終了で URL を破棄する)
  %s          リクエストを記録して表示する(ブラウザで http://127.0.0.1:4040 も開ける)
  %s   公開 URL に Basic 認証をかける(環境変数 DELA_AUTH でも指定可)
  %s   切断されても自動で再接続しない
  %s          同じ名前で複数のプロセスを同時に起動し、サーバー側で振り分ける(全員に付ける)
  %s   Prometheus 形式の /metrics を 127.0.0.1:<port> で公開する
  %s          接続の生ログも表示する

%s
  %s              このPCから起動中のトンネル一覧を表示する
  %s     PIDを指定してトンネルを停止する
  %s          すべてのトンネルを停止する
  %s              このプロジェクトの URL を表示する
  %s             サブドメインを作り直す(古い URL は無効になる)
  %s           バージョンを表示する
  %s    ログイン時に自動で起動するサービスを登録・削除・確認する(例: dela service install 3000)
  %s             GitHub Releases から最新版に更新する(--check で確認だけ)

初回の実行で、プロジェクトのフォルダ(git ならルート)に .delaconfig ができ、サーバーが発行した
サブドメインが保存されます。以降は同じプロジェクトなら毎回同じ URL(app-xxxx.try.lapius7.si)です。
.delaconfig には秘密の token が入っています(git の場合は .gitignore に自動で追記します)。
--config <ファイルのパス>(環境変数 DELA_CONFIG でも指定可。先頭の ~ は展開)を付けると、.delaconfig の代わりに
そのファイルを使います(フォルダを探さず、無ければそこに作ります。dela info / renew も同じ指定で同じファイルを見ます)。
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
		cyan("dela tcp <port>"),
		cyan("-n, --name <名前>"),
		cyan("-t, --temp"),
		cyan("-i, --inspect"),
		cyan("--auth <user:pass>"),
		cyan("--no-reconnect"),
		cyan("--replica"),
		cyan("--metrics-port <port>"),
		cyan("-v, --verbose"),
		bold("管理コマンド:"),
		cyan("dela list [--json]"),
		cyan("dela stop <PID>"),
		cyan("dela stop --all"),
		cyan("dela info [--json]"),
		cyan("dela renew"),
		cyan("dela version"),
		cyan("dela service install|uninstall|status"),
		cyan("dela update"),
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

	kind := ""
	if opts.tcp {
		kind = "tcp"
	}
	if !temp {
		if recs, err := listTunnels(); err == nil {
			if d, dup := findDuplicate(recs, proj.ID, label, kind, opts.replica); dup {
				fail(duplicateError(d, opts.replica))
			}
		}
	}
	tcpPort := 0
	if opts.tcp {
		tcpPort, err = registryTCPPort(proj, label)
		if err != nil {
			fail(err)
		}
	}

	projDir := ""
	if projPath != "" {
		projDir = projectKey(projPath)
	}

	knownHosts, err := writeKnownHosts()
	if err != nil {
		fail(err)
	}
	defer os.Remove(knownHosts)

	// 認証・inspect・メトリクスを使うときは、間にローカルのプロキシを挟む(再接続しても同じものを使い続ける)
	forward := target
	var extra []string
	var mt *metrics
	if opts.metricsPort > 0 {
		mt = newMetrics()
		ms, err := startMetrics(mt, opts.metricsPort)
		if err != nil {
			fail(fmt.Errorf("メトリクスの待ち受けを開始できませんでした: %w", err))
		}
		defer ms.close()
		extra = append(extra, "📈  metrics: http://"+ms.Addr+"/metrics")
	}
	if !opts.tcp && (opts.auth != "" || opts.inspect || mt != nil) {
		px, err := startProxy(target, opts, mt)
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
	if opts.replica {
		extra = append(extra, "🔀  replica: 同じ名前の他のプロセスと一緒にサーバー側で振り分けられます")
	}

	out(dim("dela · "+tunnelHost) + "\n\n")

	everConnected := false
	failures := 0
	backoff := time.Second
	for {
		connected, err := session(ctx, sessionArgs{
			proj: proj, label: label, target: target, forward: forward, knownHosts: knownHosts,
			verbose: verbose, first: !everConnected, extra: extra, dir: projDir, temp: temp,
			tcpPort: tcpPort, replica: opts.replica, metrics: mt,
		})
		mt.setConnected(false, false)
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
	dir                    string // .delaconfig のあるフォルダ(--temp では空)
	temp                   bool
	tcpPort                int // 0 以外なら TCP 転送(レジストリが割り当てた公開ポート)
	replica                bool
	metrics                *metrics
}

// tcpEstablishWait は TCP 転送で、sish の案内行が読めなくても「ssh が生きていれば確立済み」とみなすまでの待ち時間。
// ExitOnForwardFailure=yes なので、転送に失敗した ssh はここまでに終了する。
const tcpEstablishWait = 16 * time.Second // ssh の ConnectTimeout(15 秒)より長くする

// sshArgs は ssh に渡す引数を組み立てる(テストできるよう session から分けている)
func sshArgs(a sessionArgs) []string {
	remote := a.label + ":80:" + a.forward
	if a.tcpPort > 0 {
		remote = fmt.Sprintf("%d:%s", a.tcpPort, a.target)
	}
	return []string{
		"-p", tunnelPort,
		"-l", a.proj.ID,
		"-o", "PreferredAuthentications=password",
		"-o", "NumberOfPasswordPrompts=1",
		"-o", "UserKnownHostsFile=" + a.knownHosts,
		"-o", "GlobalKnownHostsFile=" + os.DevNull,
		"-o", "StrictHostKeyChecking=yes",
		"-R", remote,
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
	}
}

// tcpPortRe は sish が案内する公開アドレス(<ホスト>:<ポート>)を拾う
var tcpPortRe = regexp.MustCompile(regexp.QuoteMeta(tunnelHost) + `:(\d{1,5})\b`)

// session は ssh を1回起動し、切れるまで待つ。URL が確立したら connected=true を返す。
func session(ctx context.Context, a sessionArgs) (connected bool, err error) {
	cmd := exec.CommandContext(ctx, "ssh", sshArgs(a)...)
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

	// 確立の通知は、出力を読むループとタイマー(TCP のみ)の両方から呼ばれるので、1回だけ・排他で実行する
	var mu sync.Mutex
	printed := false
	announce := func(url string) bool {
		mu.Lock()
		defer mu.Unlock()
		if printed {
			return false
		}
		printed = true
		sp.stop()
		a.metrics.setConnected(true, !a.first)
		if a.first {
			printBanner(url, a.target, a.extra)
			rec := tunnelRecord{URL: url, Target: a.target, Name: a.label, Dir: a.dir, Temp: a.temp, ID: a.proj.ID, Replica: a.replica}
			if a.tcpPort > 0 {
				rec.Kind = "tcp"
			}
			registerTunnel(rec)
		} else {
			out(fmt.Sprintf("  %s 再接続しました: %s\n", green("✓"), cyan(url)))
		}
		return true
	}
	isPrinted := func() bool {
		mu.Lock()
		defer mu.Unlock()
		return printed
	}

	tcpURL := ""
	var estTimer *time.Timer
	if a.tcpPort > 0 {
		tcpURL = fmt.Sprintf("tcp://%s:%d", tunnelHost, a.tcpPort)
		estTimer = time.AfterFunc(tcpEstablishWait, func() { announce(tcpURL) })
		defer estTimer.Stop()
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := sanitize(scanner.Text())
		if line == "" {
			continue
		}
		if a.tcpPort > 0 {
			if m := tcpPortRe.FindStringSubmatch(line); m != nil {
				if m[1] == strconv.Itoa(a.tcpPort) {
					if announce(tcpURL) {
						continue
					}
				} else if !isPrinted() {
					estTimer.Stop() // 別ポートのままでは確立とみなさない
					sp.stop()
					out(fmt.Sprintf("  %s サーバーが別のポート(%s)を割り当てました。サーバー側の --bind-random-ports=false が必要です(docs/server.md)\n", yellow("!"), m[1]))
					if a.first {
						sp.start()
					}
				}
			}
		} else if m := urlLineRe.FindString(line); m != "" {
			if announce(m) {
				continue
			}
		}
		if !a.verbose && noisyLineRe.MatchString(line) {
			continue
		}
		if !isPrinted() {
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
	return isPrinted(), err
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

// --config <パス> / --config=<パス> を、どの位置にあっても取り出して os.Args から外す(info・renew・list・公開のすべてで使える)
func extractConfigFlag() {
	rest := os.Args[:1:1]
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--config":
			if i+1 >= len(args) {
				fail(fmt.Errorf("--config には .delaconfig の保存先のパスを指定してください"))
			}
			i++
			configFlag = args[i]
		case strings.HasPrefix(a, "--config="):
			configFlag = strings.TrimPrefix(a, "--config=")
		default:
			rest = append(rest, a)
		}
	}
	os.Args = rest
}

// duplicateError は同じ名前のトンネルが起動中のときのエラー文
func duplicateError(d tunnelRecord, replica bool) error {
	if replica && !d.Replica {
		return fmt.Errorf("同じ名前のトンネルが --replica なしで起動中です(PID %d)。一緒に振り分けるには、そちらも --replica で起動し直してください", d.PID)
	}
	return fmt.Errorf("同じ名前のトンネルが既に起動中です(PID %d)。同時に複数起動して振り分けるには、すべてに --replica を付けてください(dela list / dela stop %d で確認・停止できます)", d.PID, d.PID)
}
