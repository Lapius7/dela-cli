package main

import (
	"path/filepath"
	"strings"
	"testing"
)

var testSpec = serviceSpec{Name: "dela-app", Exe: "/opt/my tools/dela", Args: []string{"3000", "--name", "app", "--config", "/home/u/p q/.delaconfig"}}

func TestServiceName(t *testing.T) {
	if serviceName(false, "api") != "dela-api" || serviceName(true, "ssh") != "dela-tcp-ssh" {
		t.Fatal("名前の付け方")
	}
}

func TestSystemdUnit(t *testing.T) {
	u := renderSystemdUnit(testSpec)
	for _, want := range []string{
		`ExecStart="/opt/my tools/dela" "3000" "--name" "app" "--config" "/home/u/p q/.delaconfig"`,
		"Restart=always", "WantedBy=default.target",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("%q がありません:\n%s", want, u)
		}
	}
	if q := systemdQuote(`a"b\c%d$e`); q != `"a\"b\\c%%d$$e"` {
		t.Errorf("引用: %s", q)
	}
}

func TestLaunchdPlist(t *testing.T) {
	s := testSpec
	s.Args = []string{"3000", "--auth", "a&b:<c>"}
	p := renderLaunchdPlist(s, "si.lapius7.dela-app", "/tmp/x.log")
	for _, want := range []string{
		"<string>si.lapius7.dela-app</string>", "<string>/opt/my tools/dela</string>",
		"<string>a&amp;b:&lt;c&gt;</string>", "<key>KeepAlive</key>", "<key>RunAtLoad</key>", "/tmp/x.log",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("%q がありません:\n%s", want, p)
		}
	}
}

func TestWindowsTaskCommand(t *testing.T) {
	s := serviceSpec{Name: "dela-app", Exe: `C:\Program Files\dela\dela.exe`, Args: []string{"3000", "--config", `C:\Users\me\.delaconfig`}}
	want := `"C:\Program Files\dela\dela.exe" 3000 --config C:\Users\me\.delaconfig`
	if got := windowsTaskCommand(s); got != want {
		t.Errorf("got %s", got)
	}
}

func TestBuildServicePlan(t *testing.T) {
	linux, err := buildServicePlan("linux", testSpec, "/home/u", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if linux.Path != filepath.Join("/home/u", ".config", "systemd", "user", "dela-app.service") ||
		linux.InstallCmds[1][3] != "--now" || linux.StatusCmd[0] != "systemctl" {
		t.Errorf("%+v", linux)
	}
	mac, err := buildServicePlan("darwin", testSpec, "/Users/u", 501)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(mac.Path, "Library/LaunchAgents/si.lapius7.dela-app.plist") ||
		mac.InstallCmds[1][1] != "bootstrap" || mac.InstallCmds[1][2] != "gui/501" ||
		mac.UninstallCmds[0][2] != "gui/501/si.lapius7.dela-app" {
		t.Errorf("%+v", mac)
	}
	win, err := buildServicePlan("windows", testSpec, `C:\Users\u`, 0)
	if err != nil {
		t.Fatal(err)
	}
	if win.Path != "" || win.InstallCmds[0][0] != "schtasks" || win.InstallCmds[0][3] != "dela-app" || win.StatusCmd[1] != "/Query" {
		t.Errorf("%+v", win)
	}
	long := testSpec
	long.Args = []string{strings.Repeat("x", 300)}
	if _, err := buildServicePlan("windows", long, "", 0); err == nil {
		t.Error("261 文字を超える Windows のコマンドはエラー")
	}
	if _, err := buildServicePlan("plan9", testSpec, "", 0); err == nil {
		t.Error("未対応の OS はエラー")
	}
}

func TestServiceArgsFor(t *testing.T) {
	t.Setenv("DELA_AUTH", "")
	s, err := serviceArgsFor([]string{"3000", "--name", "api"}, "/abs/.delaconfig")
	if err != nil || s.Name != "dela-api" {
		t.Fatalf("%+v %v", s, err)
	}
	if got := strings.Join(s.Args, " "); got != "3000 --name api --config /abs/.delaconfig" {
		t.Errorf("引数: %s", got)
	}
	s, err = serviceArgsFor([]string{"tcp", "22", "--name", "ssh", "--replica"}, "/abs/c")
	if err != nil || s.Name != "dela-tcp-ssh" || s.Args[0] != "tcp" {
		t.Fatalf("%+v %v", s, err)
	}
	for _, args := range [][]string{{"3000", "--temp"}, {}, {"tcp"}, {"tcp", "22", "--auth", "a:b"}} {
		if _, err := serviceArgsFor(args, "/abs/c"); err == nil {
			t.Errorf("%v はエラー", args)
		}
	}
}

func TestParseServiceTarget(t *testing.T) {
	tcp, label, err := parseServiceTarget([]string{"tcp", "--name", "ssh"})
	if err != nil || !tcp || label != "ssh" {
		t.Errorf("%v %s %v", tcp, label, err)
	}
	if _, label, err := parseServiceTarget(nil); err != nil || label != "app" {
		t.Errorf("既定: %s %v", label, err)
	}
	if _, _, err := parseServiceTarget([]string{"--name", "BAD_"}); err == nil {
		t.Error("不正な名前")
	}
	if _, _, err := parseServiceTarget([]string{"foo"}); err == nil {
		t.Error("不明な引数")
	}
}
