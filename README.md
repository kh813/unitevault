# UniteVault

Obsidian Vault（Markdownファイル群）を Mac / iPhone 間で安全に利用しつつ、最終的なバックアップ先として Google Drive を用いる同期・ミラーリングツール。

## 特徴
- **iCloud同期との協調**: ファイル実体のマルチデバイス同期は既存のiCloud Driveを利用
- **独自ログと3-way merge**: `git merge-file`を活用した競合自動検出・マージ
- **Google Driveへ一方向ミラー**: `rclone sync` による一方向バックアップ
- **メニューバー / トレイ常駐GUI**: Macメニューバーに常駐し、同期ステータスの表示や「Sync Now」による手動同期が可能
- **rclone自動ダウンロード機能**: 未インストールの場合は最適な`rclone`バイナリを自動取得
- **単一バイナリ / .app バンドル動作**: Go言語で実装されており追加ランタイム不要

## クイックスタート

### 1. 前提条件
- **Git**: 3-way merge（`git merge-file`）に使用
- **rclone 設定**:
  - `rclone` バイナリが未インストールの場合は `unitevault` が実行時に最適な `rclone` を自動ダウンロードします。
  - 事前に `rclone config` （または手動DLしたrclone）で Google Drive のリモート名（例: `gdrive`）を設定・認証してください。

### 2. インストール・セットアップ

[Releases](https://github.com/kh813/unitevault/releases) ページから `UniteVault-mac-arm64.app.zip` (または CLI単体用 `unitevault-mac-arm64`) をダウンロードし、解凍して `Applications` フォルダへ配置します。

#### macOSでの初回起動時の注意点
Apple Silicon Mac等で Gatekeeper 警告が出た場合は、初回のみ以下のように属性を除去して起動してください：
```bash
xattr -d com.apple.quarantine UniteVault-mac-arm64.app
```
または、アプリを右クリック（Control+クリック）して「開く」を選択してください。

#### 初期化（`init`）
```bash
./unitevault init -vault "/Users/username/Library/Mobile Documents/iCloud~md~obsidian/Documents/YourVault" -remote gdrive -remote-path VaultBackup
```

### 3. 使用方法

#### 同期サイクルの実行（`run`）
```bash
./unitevault run
```
cron または タスクスケジューラ等に登録して定期実行（例: 2分間隔）させることを推奨します。

#### ノード状態の確認（`status`）
```bash
./unitevault status
```

#### プライマリノードへの手動昇格（`promote`）
```bash
./unitevault promote
```

## ドキュメント
- [unitevault-spec.md](unitevault-spec.md) - 詳細仕様書
- [unitevault-todo.md](unitevault-todo.md) - 実装ToDoリスト
