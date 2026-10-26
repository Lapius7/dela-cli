# dela (deplapius) — try.lapius7.si 用トンネルCLI

`try.cloudflare.com`の`cloudflared tunnel --url`相当を、自前のトンネルサーバー
(`try.lapius7.si`、実体は[sish](https://github.com/antoniomika/sish))に対して行うCLI。
ローカルのポートを実行するだけで `https://xxxx.try.lapius7.si` として即座に公開できる。

```
dela 3000
```

```
dela · try.lapius7.si

  ✓ トンネルを確立しました

    🔗  https://cc9z4sx0.try.lapius7.si
        → localhost:3000

  Ctrl+C で終了 · 他のターミナルからは dela list / dela stop で確認・停止できます
```

接続待ちの間はスピナーが回り、確立すると上のようなパネルが表示される(色付き。`NO_COLOR`環境変数か
非TTY出力では自動的に無色になる)。サブドメインはプロジェクトごとにサーバーが発行し、`.delaconfig`に保存されるので毎回同じURLで公開できる(詳細は「プロジェクトごとのURL」)。`Ctrl+C`で
トンネルを閉じるとURLは次の起動まで無効になる。サーバー側は検索エンジンにインデックスされないよう
`noindex`ヘッダー・`robots.txt`を返す。

## 前提

- システムに`ssh`コマンドがあること(Windows 10/11・macOS・Linuxに標準搭載)。
- **登録も SSH 鍵も不要**(誰でも使える公開トンネル)。初回の実行でサーバーがサブドメインを発行し、`.delaconfig`に保存する。手元の鍵・ssh-agent は使われない。`ssh` は 8.4 以降が必要。HTTP(S) のみで、TCP 転送は使えない。IP ごとに同時接続数・接続頻度の上限があり、1 時間通信がないと切断される。データ量が極端に多い場合はサーバー側で制限される(1 リクエストの本文は 100MB まで、1 接続で 100MB を超えた分は 1MB/s に低速化、同一 URL の同時接続は 30 まで)。接続元 IP とサブドメインはサーバー側にログとして記録される。
- サーバーのホスト鍵はバイナリに固定されており、ホスト鍵の確認プロンプトは出ない(鍵が一致しない場合は接続を拒否する)。
  フィンガープリント:
  ```
  SHA256:TFfR22+f3m4Fpp/5u3svHfC95Srtq+OyhWqylM8ioGc
  ```
- **公開するポートには、あらかじめローカルでWebサーバー等を起動しておくこと。** `dela`はあくまで
  転送するだけなので、`localhost:3000`に何も応答するものが無いと`502 Bad Gateway`になる。

## インストール

### pnpm(推奨)

> このパッケージは **pnpm 前提**です。npm と pnpm の両方に入れると同じコマンドが二重になるため、`pnpm add -g` だけを使ってください。

```bash
pnpm add -g @lapius/dela-cli
```

Linux / macOS(x64・arm64)/ Windows(x64)のビルド済みバイナリが入る(Go 不要、Node.js 18 以降)。
更新も同じコマンド(`pnpm add -g @lapius/dela-cli`)で行う。

### インストーラースクリプト(Go でソースからビルド)

Goがインストールされている環境向け(sca-cliと同じ方式)。

#### macOS / Linux / Windows(Git Bash・WSL)

```bash
curl -fsSL https://raw.githubusercontent.com/lapius7/dela-cli/main/install.sh | bash
```

#### Windows(PowerShell)

```powershell
irm https://raw.githubusercontent.com/lapius7/dela-cli/main/install.ps1 | iex
```

#### Windows(コマンドプロンプト / cmd)

cmdは`irm`/`iex`のようなワンライナー実行に対応していないため、一度ファイルとして保存してから実行する:

```bat
curl -fsSL https://raw.githubusercontent.com/lapius7/dela-cli/main/install.bat -o install.bat && install.bat
```

(PowerShellが使える環境では、上のPowerShell版の方が手順が短い)

---

どれも内部的に`go install github.com/lapius7/dela-cli/cmd/dela@latest`を`GOPROXY=direct`で実行し、
`$(go env GOPATH)/bin`(Windowsは`%GOPATH%\bin`)に`dela`(Windowsは`dela.exe`)を配置する。
このディレクトリにPATHが通っていない場合は、インストーラーが警告を表示するので指示に従うこと。
`GOPROXY=direct`にしているのは、Goの公式モジュールプロキシがタグの無いブランチの`@latest`解決結果を
キャッシュすることがあり、更新後もしばらく古いコミットが返ることがあるため(常にGitHubから直接取得させる)。

インストーラーで入れた場合は、同じコマンドをもう一度実行すれば更新できる(`go install`は既存のバイナリを上書きする)。

## 使い方

```
dela <port>          例: dela 3000        (localhost:3000 を公開)
dela <host:port>     例: dela 127.0.0.1:8080
dela <port> -v       接続の生ログ(sishの内部メッセージ)も表示する
dela --help
```

### プロジェクトごとのURL

初回の `dela 3000` で、プロジェクトのフォルダ(gitリポジトリならそのルート、なければカレントディレクトリ)に
`.delaconfig` ファイルができる。サーバーが発行した ID(`app-xxxxxxxxxx.try.lapius7.si` の `xxxxxxxxxx` 部分)と、
その持ち主であることを示す token が入っていて、**同じプロジェクトなら毎回同じ URL** で公開される
(サブフォルダから実行しても親の `.delaconfig` を使う)。

```
dela 3000                 → https://app-xxxxxxxxxx.try.lapius7.si
dela 4000 --name api      → https://api-xxxxxxxxxx.try.lapius7.si   (同じプロジェクトで別サービス)
dela info                 このプロジェクトの URL を表示
dela renew                サブドメイン(ID)を作り直す。古い URL は無効になる
dela 3000 --temp          一時モード(下記)
```

**一時モード** `--temp`(`-t`): `.delaconfig` を読まず・作らず、使い捨ての ID でその場だけ公開する。
終了すると URL は破棄される(次回は別の URL)。サンプルの共有や、プロジェクトに設定ファイルを残したくないときに使う。

- `.delaconfig` には秘密の token が入っている。gitリポジトリでは `.gitignore` に自動で追記される。他人に渡したり、コミットしないこと。
- ID は他人には使えない(token で認証される)。`.delaconfig` を消すと次回に新しい ID が発行される(古い ID は未使用のまま 90 日で破棄される)。
- 同じ名前(`app` や `api`)のトンネルを同時に 2 つ起動することはできない。
- 別の PC で同じ URL を使いたい場合は、`.delaconfig` をそのPCのプロジェクトにコピーする。

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
- **`go install`しても変更が反映されない**: Goの公式モジュールプロキシのキャッシュが原因のことがある。
  `$env:GOPROXY = "direct"`(PowerShell)や`GOPROXY=direct`(bash)を付けて`go install`し直すこと
  (各インストーラーは既にこれを行っている)。

## 構成

`sish`を薄くラップしているだけで、SSHプロトコル自体は自前実装せずシステムの`ssh`コマンドを
サブプロセスとして呼び出す(`cmd/dela/`)。色付け・スピナーもGo標準ライブラリのみの自前実装で、
外部依存は無い(sca-cliと同じ方針)。

| ファイル | 役割 |
|---|---|
| `main.go` | エントリポイント。ssh呼び出し・出力整形・シグナル処理 |
| `style.go` | ANSI色付けヘルパー(`NO_COLOR`・非TTY自動判定) |
| `spinner.go` | 接続待ち中のスピナー表示 |
| `state.go` | `dela list`/`stop`用のローカル状態ファイル(`~/.dela/state/`)管理 |
| `project.go` | `.delaconfig`(プロジェクトID)の読み書き・サーバーでの発行/解放・`dela renew`/`info`・ssh パスワード入力(SSH_ASKPASS)の代行 |
| `commands.go` | `list`/`stop`/`version`コマンドの実装 |
| `procalive_unix.go` / `procalive_windows.go` | プロセスの生死確認(OS別実装) |

## サーバー側(参考)

`server/registry.py` は ID を発行する小さな HTTP サービス(Python 標準ライブラリのみ、SQLite)。sish の
`--authentication-password-request-url` と `--append-user-to-subdomain` で、ユーザー名=ID・パスワード=token
として認証し、公開されるホスト名を `<名前>-<ID>.<ドメイン>` に固定する。token は SHA-256 で保存する。
