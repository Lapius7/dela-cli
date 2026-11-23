package main

import (
	"errors"
	"strings"
	"testing"
)

func TestParseRunArgsHTTP(t *testing.T) {
	t.Setenv("DELA_AUTH", "")
	c, err := parseRunArgs([]string{"3000", "--name", "api", "-v", "--metrics-port=9100", "--replica"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if c.target != "localhost:3000" || c.label != "api" || !c.verbose || c.opts.metricsPort != 9100 || !c.opts.replica || c.opts.tcp {
		t.Fatalf("%+v", c)
	}
}

func TestParseRunArgsErrors(t *testing.T) {
	t.Setenv("DELA_AUTH", "")
	cases := map[string][]string{
		"no target":          {"--name", "api"},
		"bad label":          {"3000", "--name", "A_B"},
		"bad metrics port":   {"3000", "--metrics-port", "0"},
		"bad auth":           {"3000", "--auth", "nocolon"},
		"unknown option":     {"3000", "--bogus"},
		"replica with temp":  {"3000", "--replica", "--temp"},
		"missing name value": {"3000", "--name"},
	}
	for name, args := range cases {
		if _, err := parseRunArgs(args, false); err == nil {
			t.Errorf("%s: エラーになるはず", name)
		}
	}
	if _, err := parseRunArgs([]string{"--name", "api"}, false); !errors.Is(err, errUsage) {
		t.Errorf("ポート無しは errUsage: %v", err)
	}
}

func TestParseRunArgsTCP(t *testing.T) {
	t.Setenv("DELA_AUTH", "demo:pw") // 環境変数の DELA_AUTH は TCP では無視する
	c, err := parseRunArgs([]string{"22", "--name", "ssh"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !c.opts.tcp || c.opts.auth != "" || c.target != "localhost:22" {
		t.Fatalf("%+v", c)
	}
	for _, args := range [][]string{{"22", "--auth", "a:b"}, {"22", "--auth=a:b"}, {"22", "--inspect"}, {"22", "--inspect-port", "5050"}} {
		if _, err := parseRunArgs(args, true); err == nil || !strings.Contains(err.Error(), "HTTP 専用") {
			t.Errorf("%v は TCP で拒否される: %v", args, err)
		}
	}
	if _, err := parseRunArgs([]string{"22", "--metrics-port", "9100", "--replica"}, true); err != nil {
		t.Errorf("TCP でも metrics・replica は使える: %v", err)
	}
}

func TestSSHArgsRemoteForward(t *testing.T) {
	a := sessionArgs{label: "api", forward: "127.0.0.1:5555", target: "localhost:3000", knownHosts: "kh"}
	if got := remoteForward(sshArgs(a)); got != "api:80:127.0.0.1:5555" {
		t.Errorf("HTTP: %s", got)
	}
	a.tcpPort = 20123
	a.target = "localhost:22"
	if got := remoteForward(sshArgs(a)); got != "20123:localhost:22" {
		t.Errorf("TCP: %s", got)
	}
}

func remoteForward(args []string) string {
	for i, a := range args {
		if a == "-R" {
			return args[i+1]
		}
	}
	return ""
}

func TestTCPPortRe(t *testing.T) {
	m := tcpPortRe.FindStringSubmatch("Service can be accessed on: " + tunnelHost + ":20123")
	if m == nil || m[1] != "20123" {
		t.Fatalf("%v", m)
	}
	if tcpPortRe.MatchString("other.example.com:20123") {
		t.Error("他のホストは拾わない")
	}
}

func TestParseTCPPortResponse(t *testing.T) {
	if p, err := parseTCPPortResponse(200, []byte(`{"port":20123}`)); err != nil || p != 20123 {
		t.Errorf("%d %v", p, err)
	}
	for _, c := range []struct {
		status int
		body   string
	}{{200, `{}`}, {200, `x`}, {200, `{"port":70000}`}, {401, ``}, {404, ``}, {429, ``}, {500, ``}} {
		if _, err := parseTCPPortResponse(c.status, []byte(c.body)); err == nil {
			t.Errorf("%d %q はエラー", c.status, c.body)
		}
	}
}

func TestFindDuplicate(t *testing.T) {
	recs := []tunnelRecord{
		{PID: 1, ID: "abc", Name: "app", Kind: ""},
		{PID: 2, ID: "abc", Name: "app", Kind: "tcp"},
		{PID: 3, Name: "app"}, // 古い記録(ID なし)は判定に使わない
	}
	if d, ok := findDuplicate(recs, "abc", "app", "", false); !ok || d.PID != 1 {
		t.Errorf("HTTP の重複: %v %v", d, ok)
	}
	if d, ok := findDuplicate(recs, "abc", "app", "tcp", false); !ok || d.PID != 2 {
		t.Errorf("TCP は別扱い: %v %v", d, ok)
	}
	if _, ok := findDuplicate(recs, "abc", "api", "", false); ok {
		t.Error("名前が違えば重複ではない")
	}
	if _, ok := findDuplicate(recs, "zzz", "app", "", false); ok {
		t.Error("ID が違えば重複ではない")
	}
	if e := duplicateError(recs[0], false); !strings.Contains(e.Error(), "--replica") {
		t.Error("案内に --replica を含む")
	}
	if e := duplicateError(recs[0], true); !strings.Contains(e.Error(), "--replica なし") {
		t.Error("既存が replica でない場合の案内")
	}
}

func TestToTunnelJSONKind(t *testing.T) {
	if j := toTunnelJSON(tunnelRecord{URL: "https://app-x." + tunnelHost}); j.Kind != "http" || j.Replica {
		t.Errorf("%+v", j)
	}
	if j := toTunnelJSON(tunnelRecord{URL: "tcp://" + tunnelHost + ":20000", Name: "ssh", Kind: "tcp", Replica: true}); j.Kind != "tcp" || !j.Replica || j.Name != "ssh" {
		t.Errorf("%+v", j)
	}
}

func TestFindDuplicateReplica(t *testing.T) {
	recs := []tunnelRecord{{PID: 1, ID: "abc", Name: "app", Replica: true}, {PID: 2, ID: "abc", Name: "app"}}
	if _, ok := findDuplicate(recs[:1], "abc", "app", "", true); ok {
		t.Error("両方 --replica なら共存できる")
	}
	if d, ok := findDuplicate(recs, "abc", "app", "", true); !ok || d.PID != 2 {
		t.Errorf("replica でない記録が1つでもあれば衝突: %v %v", d, ok)
	}
	if _, ok := findDuplicate(recs[:1], "abc", "app", "", false); !ok {
		t.Error("新しい起動が --replica でなければ衝突")
	}
}
