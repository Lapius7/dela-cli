package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnvConfigPath(t *testing.T) {
	home, _ := os.UserHomeDir()
	t.Setenv("DELA_CONFIG", "")
	configFlag = ""
	if envConfigPath() != "" {
		t.Fatal("未指定なら空")
	}
	configFlag = "~/.dela/projects/api.delaconfig"
	if want := filepath.Join(home, ".dela", "projects", "api.delaconfig"); envConfigPath() != want {
		t.Fatalf("~ を展開する: %s", envConfigPath())
	}
	configFlag = ""
	t.Setenv("DELA_CONFIG", "/tmp/x.delaconfig")
	if envConfigPath() != "/tmp/x.delaconfig" {
		t.Fatal("環境変数")
	}
	configFlag = "/tmp/y.delaconfig"
	if envConfigPath() != "/tmp/y.delaconfig" {
		t.Fatal("フラグが優先")
	}
	configFlag = ""
}
