# UniteVault

Obsidian Vault（Markdownファイル群）を Mac / iPhone 間で安全に利用しつつ、最終的なバックアップ先として Google Drive を用いる同期・ミラーリングツール。

## 特徴
- **iCloud同期との協調**: ファイル実体のマルチデバイス同期は既存のiCloud Driveを利用
- **独自ログと3-way merge**: `git merge-file`を活用した競合自動検出・マージ
- **Google Driveへ一方向ミラー**: `rclone sync` による一方向バックアップ
- **単一バイナリ動作**: Go言語で実装されており追加ランタイム不要

## クイックスタート

### 1. 前提条件のインストール
- **rclone**: Google Driveミラーリングに使用
  - Mac: `brew install rclone`
  - Windows: [rclone.org](https://rclone.org/downloads/) からダウンロード
  - `rclone config` を実行し、Google Drive のリモート名（例: `gdrive`）を設定してください。
- **Git**: 3-way merge（`git merge-file`）に使用

### 2. インストール・セットアップ

[Releases](https://github.com/kh813/unitevault/releases) ページからお使いのOS向けバイナリをダウンロードします。

#### macOSでの初回起動時の注意点
Apple Silicon Mac等で Gatekeeper 警告や属性除去を行うには、初回のみ以下のコマンドを実行してください：
```bash
xattr -d com.apple.quarantine unitevault-mac-arm64
chmod +x unitevault-mac-arm64
```

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
