# UniteVault

Obsidian Vault（Markdownファイル群）を Mac / iPhone / Windows 間で安全に利用しつつ、最終的なバックアップ先として Google Drive を用いる同期・バックアップツールです。

## 特徴

- **iCloud同期との協調**: ファイル実体の端末間同期は既存のiCloud Driveを利用（このアプリは端末を跨いだファイルコピーは行いません）
- **独自ログと3-way merge**: 複数端末が同じファイルを編集した場合の競合を、`git merge-file`を使って自動検出・マージ
- **Google Driveへ一方向バックアップ**: `rclone sync` によるミラー転送
- **単一ウィンドウの設定画面**: メニューバー（Mac）／タスクトレイ（Windows）に常駐し、Settings画面から設定・状態確認・Google Drive接続をすべて行えます
- **Git / rclone 自動インストール**: 未インストールでもSettings画面のボタンから自動取得できます（いつ・どちらが必要かは後述）
- **単一バイナリ / .app バンドル動作**: Go言語で実装されており追加ランタイム不要

## 動作環境

- **macOS**（Apple Silicon）または **Windows 10/11**
- すでに iCloud Drive 上で使っている（または使う予定の）Obsidian Vault
- Google アカウント（Google Driveへのバックアップに使用）
- Git・rcloneは事前インストール不要です（後述の手順内でアプリが自動的にインストールを案内します）

iPhone/iPad側は追加インストール不要です。iCloud同期のみで完結します。

---

## はじめての方向け・セットアップ手順

### 1. ダウンロード

[Releases](https://github.com/kh813/unitevault/releases) ページから最新版をダウンロードします。

- **Mac**: `UniteVault-mac-arm64.app.zip` をダウンロードして解凍し、`UniteVault.app` を `Applications` フォルダへ移動します。
- **Windows**: `UniteVault-windows-amd64.zip` をダウンロードして、任意のフォルダに解凍します（`UniteVault.exe` が展開されます）。

### 2. 初回起動時の警告への対処

初回起動時のみ、OSのセキュリティ機能により警告が表示されます。これはアプリが未購入の開発者証明書（アドホック署名）でビルドされているためで、故障や異常ではありません。

**Mac（「"UniteVault"は壊れているため開けません」または開発元未確認の警告）**:
- `UniteVault.app` を **右クリック（Controlキー+クリック）** → 「開く」を選択し、表示される確認ダイアログでも「開く」を選択してください。
- それでも「壊れている」表示が出る場合は、ターミナルで以下を一度だけ実行してから再度開いてください。
  ```bash
  xattr -d com.apple.quarantine /Applications/UniteVault.app
  ```

**Windows（「WindowsによってPCが保護されました」という SmartScreen 警告）**:
- 警告画面の「詳細情報」をクリックし、「実行」を選択してください。

### 3. 起動 → Settingsウィンドウ

`UniteVault.app`（Mac）または `UniteVault.exe`（Windows）をダブルクリックして起動します。

- Mac: メニューバーに同期矢印のアイコンが表示されます
- Windows: タスクトレイに同期矢印のアイコンが表示されます

初回起動時は未設定のため、**Settingsウィンドウが自動的に開きます**（メニューバー／タスクトレイアイコンをクリックし、「Settings...」からいつでも再度開けます）。

### 4. Obsidian Vaultを指定

「Obsidian Vault」セクションの **[ Select Folder ]** ボタンから、iCloud Drive上のObsidian Vaultフォルダを選択します。

> Windowsで、Vaultをこの端末とiPhone間でも同期したい場合は、事前に「iCloud for Windows」をインストールし、Vaultフォルダを iCloud Drive 配下に置いてください（未インストールの場合、この時点で案内ダイアログが表示されます）。

### 5. Google Driveへの接続

「rclone」セクションの **[ Configure Google Drive Remote... ]** ボタンを押します。

1. 「New Setup (Recommended)」を選択します
2. ブラウザが自動的に開くので、Googleアカウントでログインし、アクセスを許可します
3. 「Google Drive Connected」と表示されれば接続完了です

（すでにお使いの rclone 設定を使いたい場合は「Existing / CLI Config」を選ぶと、ターミナル／PowerShellでの対話設定に切り替わります。）

Remote Name・Sync Interval はデフォルト値のままで問題ありません。**Google Drive Target Folder Pathは選択したVaultフォルダ名が自動的に入力される**ので、通常は変更不要です（後述のVault変更時の注意も参照）。

### 6. 保存

**[ Save Settings ]** を押します。自動的に以下が行われます。

- 設定の保存
- **Primary / Secondary の自動判定**: Google Drive上に他端末の初期化情報（`PRIMARY_MARKER.json`）が無ければこの端末が Primary（同期エンジン実行・Google Drive転送を担当）、既にあれば Secondary（編集のみ）になります。手動で選ぶ必要はありません。

保存が完了すると要約ダイアログが表示され、Settingsウィンドウは閉じます。以降はバックグラウンドで自動的に同期サイクル（デフォルト120秒間隔）が実行されます。

### 7. 日常的な使い方

メニューバー／タスクトレイのメニュー項目：

- **Status: ...**: 現在の状態（`Not Initialized` / `Active (primary)` / `Syncing...` / `Error (...)`）
- **Sync Now**: 次の定期実行を待たずに即座に同期を1回実行
- **Settings...**: 設定の確認・変更
- **Quit UniteVault**: 終了

Primary端末（基本的にMac、または最初にセットアップした端末）は、他端末の変更をマージしてGoogle Driveへ反映する役割を担うため、**定期的に起動しておく**ことを推奨します（起動していない間の他端末の変更は、Primaryが次に起動するまで反映されません）。

### 8. 2台目以降の端末を追加する

同じObsidian VaultをiCloud経由で使っている別のMac／Windows機にもUniteVaultをインストールし、手順3〜6を同様に行ってください。Google Drive上に既に `PRIMARY_MARKER.json` が存在するため、自動的に **Secondary** として初期化され、既存のVault・ログ一式が `rclone copy` でその端末にも取得されます。

iPhone/iPadは追加インストール不要（iCloud同期のみ）です。

### 9. Vaultを変更する場合の注意

Google Driveへのバックアップは`rclone sync`（同期先を同期元と完全一致させるミラー転送）で行われます。そのため、**同期するVaultを別のフォルダに切り替える際、Google Drive Target Folder Pathを前と同じにしたままにすると、次回の同期で以前のVaultのバックアップファイルが削除され、新しいVaultの内容で上書きされます**。

これを防ぐため、Google Drive Target Folder Pathは選択したVaultフォルダ名を自動的に提案し、Vaultを選び直すたびに追従します（手動で変更した値は上書きされません）。基本的には何もしなくても安全ですが、意図的に同じ保存先を使い続けたい場合を除き、Target Folder Pathがちゃんと新しいVault名になっていることを保存前に確認してください。もし前回保存時と同じVaultパス・同じTarget Folder Pathの組み合わせのままVaultだけ変更して保存しようとすると、警告ダイアログが表示されます。

不要になった過去のバックアップフォルダは、Google Drive上で手動削除してください（アプリからは自動削除しません）。

---

## Git と rclone、それぞれいつ必要になるか

Settingsウィンドウの「Status」セクションから、それぞれ未インストールの場合はワンクリックでインストールできます（自動インストールに失敗した場合は公式ダウンロードページへの案内が表示されます）。ただし、実際に必要となる条件は異なります。

- **Git**: 編集端末が **2台以上** あり、同じファイルが競合編集された場合の自動マージにのみ使われます。**Windows/Mac 1台だけでObsidianを使い、Google Driveへバックアップするだけ**であれば、Gitは一度も使われません。ただし将来的に端末を追加する可能性に備え、初回セットアップ時にインストールしておくことを推奨します（未インストールのままでも動作は継続できます）。
- **rclone**: Google Driveへのバックアップに必須です。加えて、複数端末構成では上記の Primary/Secondary 判定にも使われるため、**バックアップ機能を使わない「同期のみ」構成は現状サポートしていません**。

詳細な設計根拠は [unitevault-spec.md](unitevault-spec.md) の3.6.3.1節を参照してください。

---

## トラブルシューティング

- **Git/rcloneのインストールを促すダイアログが毎回出る**: 未初期化のままGit/rcloneが未検出の場合、起動のたびに案内ダイアログが表示されます。表示不要な場合は「Don't show this again」にチェックを入れてください。
- **設定をやり直したい**: Settingsウィンドウ内の **[ Reset Configuration ]** ボタンから、ローカルの設定・端末役割情報をクリアして初期状態に戻せます（誤操作防止のため、タスクトレイメニューには配置していません）。
- **Google Driveの接続をやり直したい（別のGoogleアカウントに変更したい等）**: rcloneセクションの **[ Remove Remote Configuration... ]** ボタン（リモートが設定済みの場合のみ表示）から、確認の上でrclone側の認証情報を削除できます。Google Drive上のバックアップファイル自体は削除されません。削除後、改めて **[ Configure Google Drive Remote... ]** から設定し直せます。
- **ローカルの設定・ログの保存場所**:
  - Mac: `~/.unitevault/`（`config.json`, `device_id`, `role`, `engine.log`）
  - Windows: `%APPDATA%\unitevault\`
- 同期の詳しい仕組み（変更検出・競合解決ルール・Primary/Secondary判定など）は [unitevault-spec.md](unitevault-spec.md) を参照してください。

---

## 上級者向け：CLIでの利用

GUIを使わず、`cron` やタスクスケジューラなど自動化された環境で動かしたい場合は、CLIサブコマンドも利用できます（GUIと同じローカル設定ファイルを共有します）。

```bash
# 初期化（Vaultパス・Google Driveリモートを指定して初期化）
./unitevault init -vault "/path/to/YourVault" -remote ObsidianVault -remote-path VaultBackup

# 同期サイクルを1回だけ実行
./unitevault run --once

# 常駐デーモンとして定期実行（デフォルトの動作）
./unitevault run

# 現在の端末ID・役割・設定を確認
./unitevault status

# この端末を手動でPrimaryに昇格
./unitevault promote
```

CLIを使う場合もGitのインストールは事前に必須です（`init`/`run`は起動時にGit未検出だとエラー終了します。rcloneは未検出でも自動ダウンロードされます）。

## ドキュメント

- [unitevault-spec.md](unitevault-spec.md) - 詳細仕様書
- [unitevault-todo.md](unitevault-todo.md) - 実装ToDoリスト
