# UniteVault 実装ToDoリスト（Phase別）

仕様書（`unitevault-spec.md`）の6.5節のパッケージ構成に沿って、依存関係の少ないものから順にPhaseを分けている。各Phaseは**必ず「コンパイル確認・エラー修正」ステップで締める**（動くとわかっている状態を積み重ねてから次に進む）。

---

## Phase 0：プロジェクト初期化

- [x] `go mod init` でモジュールを作成（モジュールパス例：`github.com/<user>/unitevault`）
- [x] 6.5節のディレクトリ構成を空パッケージ（`package scan` 等の宣言のみ）で作成
  - `cmd/unitevault/main.go`
  - `internal/scan/`
  - `internal/syncedlog/`
  - `internal/merge/`
  - `internal/bootstrap/`
  - `internal/drive/`
  - `internal/config/`
- [x] `.gitignore`（ビルド成果物、`*.log`、ローカル設定ファイル等を除外）
- [x] `README.md` の雛形（後のPhaseで内容を追記していく前提の空ファイルでよい）
- [x] **コンパイル確認**：`go build ./...` を実行し、パッケージ宣言のみの状態でエラーが出ないことを確認する

---

## Phase 1：`internal/config`（ローカル設定・デバイスID）

対応する仕様書セクション：3.2節（デバイスID方式）、6.4節（ローカル設定領域）

- [x] ローカル設定ディレクトリのパス解決（Mac: `~/.unitevault/`、Windows: `%APPDATA%\unitevault\`）を実装
- [x] `config.json` の読み書き（Vaultパス、rcloneリモート名、実行間隔等のフィールド定義）
- [x] `device_id` の読み込み／未存在時のUUID自動生成・保存
- [x] `role`（primary/secondary）のキャッシュ読み書き
- [x] 簡単な単体テスト（初回生成→再読み込みでUUIDが変わらないこと、等）
- [x] **コンパイル確認**：`go build ./...` → `go test ./internal/config/...` を実行し、エラー・テスト失敗を解消する

---

## Phase 2：`internal/scan`（ハッシュ計算・変更検出・リネーム推測）

対応する仕様書セクション：3.3.0節、3.4.1節（デバウンス方式）、3.6.2節（改行コード正規化）

- [x] 改行コードLF正規化関数の実装（比較・記録の直前に必ず通す）
- [x] Vault内全ファイルのスキャン（`path/filepath` でOS差異を吸収）
- [x] ファイルごとの内容ハッシュ算出（`crypto/sha256`）
- [x] 前回スキャン結果（`_sync/state/last_scan.json`）との比較ロジック
  - 新規／削除／変更の検出
  - 消えたパス × 新規パスでハッシュ完全一致 → `rename` と推測
- [x] デバウンス判定（2回連続で同一ハッシュのファイルのみ「確定」扱いにする）
- [x] 単体テスト（一時ディレクトリを使い、ファイル追加・削除・リネーム・変更なしの各パターンを検証）
- [x] **コンパイル確認**：`go build ./...` → `go test ./internal/scan/...` を実行し、エラー・テスト失敗を解消する

---

## Phase 3：`internal/syncedlog`（デバイス別ログの読み書き）

対応する仕様書セクション：3.2節（ログスキーマ）

- [x] ログエントリ構造体の定義（`device`, `label`, `seq`, `path`, `base_hash`, `result_hash`, `diff`, `action`, `ts`、および`rename`用の`old_path`/`new_path`）
- [x] JSON Lines形式での追記書き込み（`log-<device-uuid>.jsonl`）
- [x] JSON Lines形式での全件読み込み・パス毎の最新エントリ抽出
- [x] Phase 2（scan）の検出結果からログエントリを生成する変換処理
- [x] 単体テスト（追記→読み込みで内容が一致すること、複数デバイスのログを混在させても正しく最新エントリが取れること）
- [x] **コンパイル確認**：`go build ./...` → `go test ./internal/syncedlog/...` を実行し、エラー・テスト失敗を解消する

---

## Phase 4：`internal/drive`（rcloneラッパー・リトライ制御）

対応する仕様書セクション：3.5節、3.5.1節（リトライ方針）

- [x] `rclone sync` / `rclone copy` を `os/exec` で呼び出す薄いラッパー実装
- [x] 指数バックオフでの再試行（30秒→2分→10分、計3回）
- [x] 失敗時のログ出力（`engine.log`へ書き込み、次回実行への持ち越し）
- [x] rcloneの標準エラー出力・終了コードのハンドリング
- [x] 簡単な結合テスト（rcloneが実機に入っている環境限定でよい。CIでは`rclone`コマンドの有無をチェックしてskipする形にする）
- [x] **コンパイル確認**：`go build ./...` を実行し、エラーを解消する（rclone実機テストはローカル環境でのみ実行）

---

## Phase 5：`internal/bootstrap`（プライマリ／セカンダリ自動判定）

対応する仕様書セクション：3.6.1.1節、3.6.1.2節、6.3節（PRIMARY_MARKER.jsonの複製に関する注意）

- [x] `PRIMARY_MARKER.json` の構造体定義（`schema_version`, `primary_device_id`, `primary_label`, `initialized_at`, `vault_root_hash`, `app_version`）
- [x] Phase 4（drive）を使い、Google Drive上のマーカー有無を確認する処理
- [x] マーカーが存在しない場合：プライマリとして初期化し、マーカーをアップロード後に再ダウンロードして自分自身のIDと一致するか検証するレース条件対策を実装
- [x] マーカーが存在する場合：セカンダリとして`rclone copy`でVault一式・全ログをローカルへ取得する処理
- [x] 6.3節の対策：プライマリはローカルVault内`_sync/`にも`PRIMARY_MARKER.json`の複製を保存する処理
- [x] プライマリノード移行処理（3.6.1.2節、手動手順を叩くための最小限のコマンド化）
- [x] 単体テスト（drive部分はインターフェースで抽象化してモック可能にし、マーカー有無それぞれの分岐をテストする）
- [x] **コンパイル確認**：`go build ./...` → `go test ./internal/bootstrap/...` を実行し、エラー・テスト失敗を解消する

---

## Phase 6：`internal/merge`（3-way merge・N台対応）

対応する仕様書セクション：3.3節

- [x] `git merge-file` を `os/exec` で呼び出すラッパー実装（一時ファイルの作成・後始末を含む）
- [x] `base_hash` を突き合わせて分岐点を特定するロジック
- [x] `base_hash` が一致しない場合に、間のdiffを遡って適用するロジック
- [x] 3台以上の変更を順に3-way mergeで畳み込むN-way mergeロジック
- [x] コンフリクトマーカー（`<<<<<<<` 等）の検出
- [x] 単体テスト（2台のみ・3台競合・非競合の自動マージ、それぞれのケースを検証）
- [x] **コンパイル確認**：`go build ./...` → `go test ./internal/merge/...` を実行し、エラー・テスト失敗を解消する

---

## Phase 7：コンフリクト解消CLI

対応する仕様書セクション：3.5.2節

- [x] コンフリクトマーカーを含む箇所を抽出し、Terminal.app / PowerShellへ分かりやすく表示する処理
- [x] ユーザーに「どのデバイスの内容を採用するか」「手動編集して確定するか」を選ばせる対話処理
- [x] 選択結果をVault現物ファイルへ反映し、新たなログエントリとして記録する処理（Phase 3のsyncedlogを利用）
- [x] 手動テスト（実際にコンフリクトを起こして選択→反映まで一通り動作確認）
- [x] **コンパイル確認**：`go build ./...` を実行し、エラーを解消する

---

## Phase 8：エンジン本体（定期実行ループの統合）

対応する仕様書セクション：4節（運用フロー）全体

- [x] Phase 1〜7のパッケージを組み合わせ、4節の運用フロー（①変更検出→②ログ追記→③マージ→④コンフリクト提示→⑤Drive転送）を1回分の処理として実装
- [x] プライマリ／セカンダリで実行内容を分岐させる処理（セカンダリはログ追記のみ、マージ・Drive転送は行わない）
- [x] 常駐型デーモンプロセス（一定間隔でのループ実行）およびシグナルハンドリング（SIGINT/SIGTERM等での安全終了）を実装
- [x] 結合テスト（複数の一時ディレクトリをデバイス代わりに見立て、Vault→ログ→マージ→（drive部分はモック）までを一通り流す）
- [x] **コンパイル確認**：`go build ./...` → `go test ./...` を実行し、全パッケージでエラー・テスト失敗を解消する

---

## Phase 9：`cmd/unitevault/main.go`（CLIとしての配線）

- [x] サブコマンド構成の決定・実装（`unitevault init`, `unitevault run`（デフォルト常駐、`--once`で単発実行）, `unitevault status`, `unitevault promote`）
- [x] フラグ・環境変数によるVaultパス等の指定
- [x] Phase 8の常駐エンジンをコマンドから呼び出す配線
- [x] `--help` 出力の整備
- [x] 手動テスト（実際にビルドしたバイナリをコマンドラインから一通り叩いて動作確認）
- [x] **コンパイル確認**：`go build ./...` を実行し、エラーを解消する。加えて `go vet ./...` も通しておく

---

## Phase 10：ビルド・配布パイプライン（CI）

対応する仕様書セクション：3.6.6節（アドホック署名を含む）

- [x] GitHub Actionsワークフロー（`.github/workflows/release.yml`）の作成
- [x] Mac用（arm64/amd64）・Windows用のクロスコンパイル
- [x] Macバイナイルへのアドホック署名（`codesign --force --deep --sign -`）と`codesign --verify`による検証をCIのステップに含める
- [x] GitHub Releasesへの成果物アップロード
- [x] タグpush等をトリガーにした自動実行の設定
- [x] **コンパイル確認**：CI上での`go build ./...`が成功することを実際にワークフローを回して確認する（ローカルとCI環境の差異がないか確認する意味も込めて）

---

## Phase 11：手動結合テスト・実機検証

- [x] Mac単体での動作確認（初回起動→プライマリ初期化→Google Driveへの転送まで）
- [x] 2台目（別Mac or Windows）をセカンダリとして参加させ、`rclone copy`によるクローンが正しく動くか確認
- [x] 意図的な競合編集（同じファイルを2台でオフライン編集）を起こし、コンフリクトCLIでの解消が正しく反映されるか確認
- [x] iCloud for Windowsの導入・動作確認（5節Todoの実施）
- [x] rcloneのネットワーク断・認証切れ時のリトライ・スキップ挙動の確認
- [x] Macでの初回起動時、アドホック署名＋検疫属性除去の案内通りに起動できるか確認（3.6.6.3節）
- [x] Windowsでの初回起動時、SmartScreen警告の案内通りに実行できるか確認
- [x] **コンパイル確認**：この段階で見つかった不具合を修正した場合は、その都度 `go build ./...` → `go test ./...` を実行し、regressionが無いことを確認する

---

## Phase 12：ドキュメント整備

- [x] `README.md` にセットアップ手順を記載（前提ソフトのインストール、`rclone config`の実行、初回起動、署名回避策の案内を含む）
- [x] 仕様書（`unitevault-spec.md`）とREADMEの内容に矛盾がないか突き合わせる
- [x] 5節（将来タスク）に記載の項目のうち、今回のスコープ外としたものを`README.md`の「今後の予定」等に転記する

---

## 進め方の補足

- 各PhaseはPhase番号の順に進めることを推奨する（Phase 4〈drive〉→Phase 5〈bootstrap〉→Phase 6〈merge〉の順は依存関係上この並びが自然）。
- ただし Phase 2（scan）・Phase 3（syncedlog）は互いに依存が薄いため、順番を入れ替えても問題ない。
- 「コンパイル確認」のステップは省略しないこと。小さい単位でコンパイルを通し続けることで、どのPhaseでエラーが混入したかを常に特定しやすくする。
