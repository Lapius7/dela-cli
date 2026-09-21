# dela (deplapius) — deploy.lapius7.com 用トンネルCLI

`try.cloudflare.com`の`cloudflared tunnel --url`相当を、自前のトンネルサーバー
(`deploy.lapius7.com`、実体は[sish](https://github.com/antoniomika/sish))に対して行うCLI。
ローカルのポートを実行するだけで `https://xxxx.deploy.lapius7.com` として即座に公開できる。

```
dela 3000
```

```
dela · deploy.lapius7.com

  ✓ トンネルを確立しました

    🔗  https://cc9z4sx0.deploy.lapius7.com
        → localhost:3000

  Ctrl+C で終了 · 他のターミナルからは dela list / dela stop で確認・停止できます
```

接続待ちの間はスピナーが回り、確立すると上のようなパネルが表示される(色付き。`NO_COLOR`環境変数か
非TTY出力では自動的に無色になる)。サブドメインは毎回ランダムに割り当てられ(固定不可)、`Ctrl+C`で
トンネルを閉じるとURLも即座に無効になる。サーバー側は検索エンジンにインデックスされないよう
`noindex`ヘッダー・`robots.txt`を返す。

## 前提

- システムに`ssh`コマンドがあること(Windows 10/11・macOS・Linuxに標準搭載)。
- 接続には**事前に登録した公開鍵**が必要(オーナー専用。未登録の鍵からは接続を拒否される)。
- 初回接続時にホスト鍵の確認が出る。フィンガープリントが以下と一致することを確認してから`yes`と答えること:
  ```
  SHA256:TFfR22+f3m4Fpp/5u3svHfC95Srtq+OyhWqylM8ioGc
  ```
- **公開するポートには、あらかじめローカルでWebサーバー等を起動しておくこと。** `dela`はあくまで
  転送するだけなので、`localhost:3000`に何も応答するものが無いと`502 Bad Gateway`になる。

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

どれも内部的に`go install github.com/lapius7/dela-cli/cli/cmd/dela@latest`を`GOPROXY=direct`で実行し、
`$(go env GOPATH)/bin`(Windowsは`%GOPATH%\bin`)に`dela`(Windowsは`dela.exe`)を配置する。
このディレクトリにPATHが通っていない場合は、インストーラーが警告を表示するので指示に従うこと。
`GOPROXY=direct`にしているのは、Goの公式モジュールプロキシがタグの無いブランチの`@latest`解決結果を
キャッシュすることがあり、更新後もしばらく古いコミットが返ることがあるため(常にGitHubから直接取得させる)。

更新したい時も同じコマンドをもう一度実行すればよい(`go install`は既存のバイナリを上書きする)。

## 使い方

```
dela <port>          例: dela 3000        (localhost:3000 を公開)
dela <host:port>     例: dela 127.0.0.1:8080
dela <port> -v       接続の生ログ(sishの内部メッセージ)も表示する
dela --help
```

### 管理コマンド

このPCで起動中のトンネルは、`~/.dela/state/`にプロセスごとの記録として残るので、
別のターミナルからも一覧・停止ができる(デーモンは無い。各`dela`プロセス自身が記録の作成・削除を行う)。

```
dela list             起動中のトンネル一覧(PID・URL・転送先・経過時間)
dela stop <PID>       指定したPIDのトンネルを停止する
dela stop --all       すべてのトンネルを停止する
dela version          バージョンを表示する
```

## トラブルシューティング

- **`502 Bad Gateway`が出る**: トンネル自体は正常に繋がっている。指定したポートに実際のサーバーが
  起動していないだけなので、まず`http://localhost:<port>/`がローカルで開けるか確認すること。
- **`Permission denied` / 接続がすぐ切れる**: 手元に複数のSSH鍵(特に`id_rsa`)がある場合、
  意図しない方の鍵が先に試されて拒否されると、そのまま接続ごと切断されることがある。
  `~/.ssh/config`に以下を追記し、`deploy.lapius7.com`宛はed25519鍵だけを使うよう固定するとよい:
  ```
  Host deploy.lapius7.com
      IdentityFile ~/.ssh/id_ed25519
      IdentitiesOnly yes
  ```
- **`go install`しても変更が反映されない**: Goの公式モジュールプロキシのキャッシュが原因のことがある。
  `$env:GOPROXY = "direct"`(PowerShell)や`GOPROXY=direct`(bash)を付けて`go install`し直すこと
  (各インストーラーは既にこれを行っている)。

## 構成

`sish`を薄くラップしているだけで、SSHプロトコル自体は自前実装せずシステムの`ssh`コマンドを
サブプロセスとして呼び出す(`cli/cmd/dela/`)。色付け・スピナーもGo標準ライブラリのみの自前実装で、
外部依存は無い(sca-cliと同じ方針)。

| ファイル | 役割 |
|---|---|
| `main.go` | エントリポイント。ssh呼び出し・出力整形・シグナル処理 |
| `style.go` | ANSI色付けヘルパー(`NO_COLOR`・非TTY自動判定) |
| `spinner.go` | 接続待ち中のスピナー表示 |
| `state.go` | `dela list`/`stop`用のローカル状態ファイル(`~/.dela/state/`)管理 |
| `commands.go` | `list`/`stop`/`version`コマンドの実装 |
| `procalive_unix.go` / `procalive_windows.go` | プロセスの生死確認(OS別実装) |
