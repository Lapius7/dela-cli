package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// dela service install|uninstall|status: ログイン時に dela を自動で起動するサービスの登録。
// Linux は systemd のユーザーユニット、macOS は launchd の LaunchAgent、Windows はタスクスケジューラを使う
// (どれも OS 標準の仕組みで、管理者権限は要らない)。

// serviceSpec はサービスとして動かす dela の内容
type serviceSpec struct {
	Name string   // 例: dela-app / dela-tcp-api
	Exe  string   // dela の実行ファイル(絶対パス)
	Args []string // dela に渡す引数(Exe は含めない)
}

// servicePlan は OS ごとの登録手順。実行はせず手順だけを作るので、どの OS の分もテストできる。
type servicePlan struct {
	Path          string     // 書き出すファイル(Windows は無し)
	Content       string     // その内容
	InstallCmds   [][]string // 書き出したあとに順に実行する(失敗したら止める)
	UninstallCmds [][]string // ファイルを消す前に実行する(失敗しても続ける)
	StatusCmd     []string
}

func serviceName(tcp bool, label string) string {
	if tcp {
		return "dela-tcp-" + label
	}
	return "dela-" + label
}

// launchdLabel は macOS の LaunchAgent のラベル
func launchdLabel(name string) string { return "si.lapius7." + name }

// buildServicePlan は goos 向けの登録手順を作る。home はユーザーのホーム、uid は macOS の gui/<uid> に使う。
func buildServicePlan(goos string, s serviceSpec, home string, uid int) (servicePlan, error) {
	switch goos {
	case "linux":
		path := filepath.Join(home, ".config", "systemd", "user", s.Name+".service")
		return servicePlan{
			Path:    path,
			Content: renderSystemdUnit(s),
			InstallCmds: [][]string{
				{"systemctl", "--user", "daemon-reload"},
				{"systemctl", "--user", "enable", "--now", s.Name + ".service"},
			},
			UninstallCmds: [][]string{
				{"systemctl", "--user", "disable", "--now", s.Name + ".service"},
			},
			StatusCmd: []string{"systemctl", "--user", "status", "--no-pager", s.Name + ".service"},
		}, nil
	case "darwin":
		label := launchdLabel(s.Name)
		path := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
		domain := fmt.Sprintf("gui/%d", uid)
		return servicePlan{
			Path:    path,
			Content: renderLaunchdPlist(s, label, filepath.Join(home, "Library", "Logs", s.Name+".log")),
			InstallCmds: [][]string{
				{"launchctl", "bootout", domain + "/" + label}, // 入れ替えのとき用。未登録なら失敗するが、後で無視される
				{"launchctl", "bootstrap", domain, path},
			},
			UninstallCmds: [][]string{{"launchctl", "bootout", domain + "/" + label}},
			StatusCmd:     []string{"launchctl", "print", domain + "/" + label},
		}, nil
	case "windows":
		tr := windowsTaskCommand(s)
		if len(tr) > 261 {
			return servicePlan{}, fmt.Errorf("コマンドが長すぎてタスクスケジューラに登録できません(%d 文字、上限 261)。--config のパスを短くしてください", len(tr))
		}
		return servicePlan{
			InstallCmds: [][]string{
				{"schtasks", "/Create", "/TN", s.Name, "/TR", tr, "/SC", "ONLOGON", "/RL", "LIMITED", "/F"},
				{"schtasks", "/Run", "/TN", s.Name},
			},
			UninstallCmds: [][]string{
				{"schtasks", "/End", "/TN", s.Name},
				{"schtasks", "/Delete", "/TN", s.Name, "/F"},
			},
			StatusCmd: []string{"schtasks", "/Query", "/TN", s.Name, "/V", "/FO", "LIST"},
		}, nil
	}
	return servicePlan{}, fmt.Errorf("この OS(%s)ではサービス登録に対応していません", goos)
}

// systemdQuote は ExecStart の1引数を systemd の書式で囲む。
// % はユニット指定子、$ は環境変数展開なので、どちらも二重にして文字どおりにする。
func systemdQuote(a string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%", "$", "$$")
	return `"` + r.Replace(a) + `"`
}

func renderSystemdUnit(s serviceSpec) string {
	parts := []string{systemdQuote(s.Exe)}
	for _, a := range s.Args {
		parts = append(parts, systemdQuote(a))
	}
	return "[Unit]\n" +
		"Description=dela tunnel (" + s.Name + ")\n" +
		"After=network-online.target\n" +
		"Wants=network-online.target\n\n" +
		"[Service]\n" +
		"ExecStart=" + strings.Join(parts, " ") + "\n" +
		"Restart=always\n" +
		"RestartSec=10\n\n" +
		"[Install]\n" +
		"WantedBy=default.target\n"
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace(s)
}

func renderLaunchdPlist(s serviceSpec, label, logPath string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + xmlEscape(label) + `</string>
	<key>ProgramArguments</key>
	<array>
`)
	for _, a := range append([]string{s.Exe}, s.Args...) {
		b.WriteString("\t\t<string>" + xmlEscape(a) + "</string>\n")
	}
	b.WriteString(`	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>` + xmlEscape(logPath) + `</string>
	<key>StandardErrorPath</key>
	<string>` + xmlEscape(logPath) + `</string>
</dict>
</plist>
`)
	return b.String()
}

// windowsTaskCommand は schtasks /TR に渡す1本のコマンド文字列。空白や引用符を含む引数は " で囲む。
func windowsTaskCommand(s serviceSpec) string {
	q := func(a string) string {
		if a != "" && !strings.ContainsAny(a, " \t\"") {
			return a
		}
		return `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
	}
	parts := []string{q(s.Exe)}
	for _, a := range s.Args {
		parts = append(parts, q(a))
	}
	return strings.Join(parts, " ")
}

// parseServiceTarget は uninstall / status の引数から、対象の種類(tcp)と名前を取り出す
func parseServiceTarget(args []string) (tcp bool, label string, err error) {
	label = defaultLabel
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "tcp":
			tcp = true
		case a == "-n" || a == "--name":
			if i+1 >= len(args) {
				return false, "", errors.New("--name には名前を指定してください(例: --name api)")
			}
			i++
			label = args[i]
		case strings.HasPrefix(a, "--name="):
			label = strings.TrimPrefix(a, "--name=")
		default:
			return false, "", fmt.Errorf("不明な引数です: %s", a)
		}
	}
	return tcp, label, validateLabel(label)
}

// serviceArgsFor は install の引数(tcp を含む)から、サービスが実行する dela の引数を作る。
// 作業フォルダに依存しないよう、.delaconfig は絶対パスの --config で固定する。
func serviceArgsFor(args []string, configPath string) (spec serviceSpec, err error) {
	tcp := len(args) > 0 && args[0] == "tcp"
	runArgs := args
	if tcp {
		runArgs = args[1:]
	}
	cfg, err := parseRunArgs(runArgs, tcp)
	if err != nil {
		if errors.Is(err, errUsage) {
			return spec, errors.New("公開するポートを指定してください(例: dela service install 3000 / dela service install tcp 22)")
		}
		return spec, err
	}
	if cfg.temp {
		return spec, errors.New("--temp はサービスに使えません(起動のたびに URL が変わるため)")
	}
	spec.Name = serviceName(tcp, cfg.label)
	spec.Args = append(append([]string{}, args...), "--config", configPath)
	return spec, nil
}

var runExternal = func(argv []string) (string, error) {
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func cmdService(args []string) {
	if len(args) == 0 {
		fail(errors.New("install / uninstall / status を指定してください(例: dela service install 3000)"))
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fail(err)
	}
	switch args[0] {
	case "install":
		serviceInstall(args[1:], home)
	case "uninstall", "status":
		tcp, label, err := parseServiceTarget(args[1:])
		if err != nil {
			fail(err)
		}
		plan, err := buildServicePlan(runtime.GOOS, serviceSpec{Name: serviceName(tcp, label)}, home, os.Getuid())
		if err != nil {
			fail(err)
		}
		if args[0] == "status" {
			text, err := runExternal(plan.StatusCmd)
			if text != "" {
				out(text + "\n")
			}
			if err != nil {
				fail(fmt.Errorf("サービス %s は登録されていないか、停止しています", serviceName(tcp, label)))
			}
			return
		}
		for _, c := range plan.UninstallCmds {
			_, _ = runExternal(c)
		}
		if plan.Path != "" {
			_ = os.Remove(plan.Path)
		}
		if runtime.GOOS == "linux" {
			_, _ = runExternal([]string{"systemctl", "--user", "daemon-reload"})
		}
		out(fmt.Sprintf("%s サービス %s を削除しました\n", green("✓"), serviceName(tcp, label)))
	default:
		fail(fmt.Errorf("不明なサブコマンドです: %s(install / uninstall / status)", args[0]))
	}
}

func serviceInstall(args []string, home string) {
	cfgPath := envConfigPath()
	if cfgPath == "" {
		if p, ok := findProjectFile(); ok {
			cfgPath = p
		}
	}
	if cfgPath == "" {
		fail(errors.New("このプロジェクトにはまだ .delaconfig がありません。先に dela <port> を一度実行するか、--config <パス> を指定してください"))
	}
	if _, err := os.Stat(cfgPath); err != nil {
		fail(fmt.Errorf("%s が見つかりません。先に dela <port> --config %s を一度実行して作ってください", cfgPath, cfgPath))
	}
	spec, err := serviceArgsFor(args, cfgPath)
	if err != nil {
		fail(err)
	}
	exe, err := os.Executable()
	if err != nil {
		fail(err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	spec.Exe = exe
	plan, err := buildServicePlan(runtime.GOOS, spec, home, os.Getuid())
	if err != nil {
		fail(err)
	}
	if plan.Path != "" {
		if err := os.MkdirAll(filepath.Dir(plan.Path), 0o755); err != nil {
			fail(err)
		}
		// --auth をコマンドラインに書いたときのため、自分だけが読める権限にする
		if err := os.WriteFile(plan.Path, []byte(plan.Content), 0o600); err != nil {
			fail(fmt.Errorf("%s を書き込めませんでした: %w", plan.Path, err))
		}
	}
	for i, c := range plan.InstallCmds {
		if text, err := runExternal(c); err != nil {
			// macOS の最初の bootout は、未登録なら失敗するのが普通なので無視する
			if runtime.GOOS == "darwin" && i == 0 {
				continue
			}
			fail(fmt.Errorf("%s の実行に失敗しました: %v\n%s", strings.Join(c, " "), err, text))
		}
	}
	out(fmt.Sprintf("%s サービス %s を登録して起動しました\n", green("✓"), spec.Name))
	if plan.Path != "" {
		out(dim("    "+plan.Path) + "\n")
	}
	switch runtime.GOOS {
	case "linux":
		out(dim("    ログアウト後も動かすには: loginctl enable-linger $USER") + "\n")
	case "windows":
		out(dim("    ログオン時に起動します(コンソールの窓が開きます)") + "\n")
	}
	if strings.Contains(spec.Exe, "node_modules") {
		out(yellow("!") + " dela が node_modules の中にあります。pnpm で更新するとパスが変わるので、更新したら dela service install をやり直してください\n")
	}
	out(dim("    確認: dela service status · 削除: dela service uninstall") + "\n")
}
