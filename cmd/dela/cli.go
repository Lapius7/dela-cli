package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// errUsage は公開するポートが指定されていないときのエラー(使い方を表示して終了する)
var errUsage = errors.New("usage")

// runConfig は dela <port> / dela tcp <port> / dela service install の引数をまとめたもの
type runConfig struct {
	target  string
	label   string
	verbose bool
	temp    bool
	opts    options
}

// parseRunArgs は公開系コマンドの引数を解釈する(tcp=true のときは TCP 用)。
// HTTP と TCP と service install で同じ解釈を使うため、main から切り出している。
func parseRunArgs(args []string, tcp bool) (runConfig, error) {
	c := runConfig{label: defaultLabel, opts: options{auth: os.Getenv("DELA_AUTH"), tcp: tcp}}
	var positional []string
	value := func(i *int, flag, hint string) (string, error) {
		if *i+1 >= len(args) {
			return "", fmt.Errorf("%s には値を指定してください%s", flag, hint)
		}
		*i++
		return args[*i], nil
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, inline, hasInline := strings.Cut(a, "=")
		switch {
		case a == "-v" || a == "--verbose":
			c.verbose = true
		case a == "-t" || a == "--temp":
			c.temp = true
		case a == "-i" || a == "--inspect":
			c.opts.inspect = true
		case a == "--no-reconnect":
			c.opts.noReconnect = true
		case a == "--replica":
			c.opts.replica = true
		case a == "-n" || a == "--name" || (hasInline && name == "--name"):
			v := inline
			if !hasInline {
				var err error
				if v, err = value(&i, "--name", "(例: --name api)"); err != nil {
					return c, err
				}
			}
			c.label = v
		case a == "--auth" || a == "--inspect-port" || a == "--metrics-port" ||
			(hasInline && (name == "--auth" || name == "--inspect-port" || name == "--metrics-port")):
			v := inline
			if !hasInline {
				var err error
				if v, err = value(&i, a, ""); err != nil {
					return c, err
				}
			} else {
				a = name
			}
			if err := setValueOption(&c.opts, a, v); err != nil {
				return c, err
			}
		case strings.HasPrefix(a, "-") && len(a) > 1:
			return c, fmt.Errorf("不明なオプションです: %s(dela --help で一覧を確認できます)", a)
		default:
			positional = append(positional, a)
		}
	}
	if err := validateLabel(c.label); err != nil {
		return c, err
	}
	if len(positional) < 1 {
		return c, errUsage
	}
	target, err := resolveTarget(positional[0])
	if err != nil {
		return c, err
	}
	c.target = target
	if tcp {
		// 環境変数 DELA_AUTH は HTTP 用なので TCP では無視する(フラグで明示されたときだけ拒否する)
		if hasFlag(args, "--auth") || hasPrefixFlag(args, "--auth=") {
			return c, errors.New("--auth は HTTP 専用です(TCP には使えません)")
		}
		c.opts.auth = ""
		if c.opts.inspect {
			return c, errors.New("--inspect は HTTP 専用です(TCP には使えません)")
		}
	} else if c.opts.auth != "" {
		if u, pw, ok := strings.Cut(c.opts.auth, ":"); !ok || u == "" || pw == "" {
			return c, fmt.Errorf("--auth は ユーザー名:パスワード の形式で指定してください(例: --auth demo:s3cret)")
		}
	}
	if c.opts.replica && c.temp {
		return c, errors.New("--replica は --temp と併用できません(一時モードは実行ごとに別の ID になり、振り分けできないため)")
	}
	return c, nil
}

func hasPrefixFlag(args []string, prefix string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, prefix) {
			return true
		}
	}
	return false
}

func setValueOption(o *options, name, val string) error {
	port := func() (int, error) {
		n, err := strconv.Atoi(val)
		if err != nil || n < 1 || n > 65535 {
			return 0, fmt.Errorf("%s には 1〜65535 の数値を指定してください", name)
		}
		return n, nil
	}
	switch name {
	case "--auth":
		o.auth = val
	case "--inspect-port":
		n, err := port()
		if err != nil {
			return err
		}
		o.inspect = true
		o.inspectPort = n
	case "--metrics-port":
		n, err := port()
		if err != nil {
			return err
		}
		o.metricsPort = n
	}
	return nil
}
