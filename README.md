# dela (deplapius) — deploy.lapius7.com 用トンネルCLI

`try.cloudflare.com`の`cloudflared tunnel --url`相当を、自前のトンネルサーバー
(`deploy.lapius7.com`、実体は[sish](https://github.com/antoniomika/sish))に対して行うCLI。
ローカルのポートを実行するだけで `https://xxxx.deploy.lapius7.com` として即座に公開できる。

```
dela 3000
```

```
🔗 https://cc9z4sx0.deploy.lapius7.com
   → localhost:3000 へ転送中
   (Ctrl+C で終了)
```

サブドメインは毎回ランダムに割り当てられ(固定不可)、`Ctrl+C`でトンネルを閉じるとURLも即座に無効になる。
サーバー側は検索エンジンにインデックスされないよう`noindex`ヘッダー・`robots.txt`を返す。

## 前提

- システムに`ssh`コマンドがあること(Windows 10/11・macOS・Linuxに標準搭載)。
- 接続には**事前に登録した公開鍵**が必要(オーナー専用。未登録の鍵からは接続を拒否される)。
- 初回接続時にホスト鍵の確認が出る。フィンガープリントが以下と一致することを確認してから`yes`と答えること:
  ```
  SHA256:TFfR22+f3m4Fpp/5u3svHfC95Srtq+OyhWqylM8ioGc
  ```

## インストール

Goがインストールされている環境向け(sca-cliと同じ方式)。

### macOS / Linux / Windows(Git Bash・WSL)

```bash
curl -fsSL https://raw.githubusercontent.com/lapius7/dela-cli/main/install.sh | bash
```

### Windows(PowerShell)

```powershell
irm https://raw.githubusercontent.com/lapius7/dela-cli/main/install.ps1 | iex
```

### Windows(コマンドプロンプト / cmd)

cmdは`irm`/`iex`のようなワンライナー実行に対応していないため、一度ファイルとして保存してから実行する:

```bat
curl -fsSL https://raw.githubusercontent.com/lapius7/dela-cli/main/install.bat -o install.bat && install.bat
```

(PowerShellが使える環境では、上のPowerShell版の方が手順が短い)

---

どれも内部的に`go install github.com/lapius7/dela-cli/cli/cmd/dela@latest`を実行し、
`$(go env GOPATH)/bin`(Windowsは`%GOPATH%\bin`)に`dela`(Windowsは`dela.exe`)を配置する。
このディレクトリにPATHが通っていない場合は、インストーラーが警告を表示するので指示に従うこと。

## 使い方

```
dela <port>        例: dela 3000        (localhost:3000 を公開)
dela <host:port>   例: dela 127.0.0.1:8080
dela --help
```

## 構成

`sish`を薄くラップしているだけで、SSHプロトコル自体は自前実装せずシステムの`ssh`コマンドを
サブプロセスとして呼び出す(`cli/cmd/dela/main.go`)。Go標準ライブラリのみ、外部依存なし。
