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

## Phase 7：コンフリクト解消CLI（後にGUI方式へ置き換え - 下記参照）

対応する仕様書セクション：3.5.2節（旧）

- [x] コンフリクトマーカーを含む箇所を抽出し、Terminal.app / PowerShellへ分かりやすく表示する処理
- [x] ユーザーに「どのデバイスの内容を採用するか」「手動編集して確定するか」を選ばせる対話処理
- [x] 選択結果をVault現物ファイルへ反映し、新たなログエントリとして記録する処理（Phase 3のsyncedlogを利用）
- [x] 手動テスト（実際にコンフリクトを起こして選択→反映まで一通り動作確認）
- [x] **コンパイル確認**：`go build ./...` を実行し、エラーを解消する

**重要：このPhaseで実装したCLI対話プロンプト（標準入力での選択）は、後に廃止・置き換えられている。** 実運用（タスクトレイ常駐GUI・常駐デーモン）では対話的な標準入力を前提にできず、実際には機能していなかった（レビューで発覚）。加えて、baseを空文字列のまま3-way mergeしていたため、重ならない箇所の編集ですら常にコンフリクト扱いになる別の不具合もあった。代わりに、(1) 各ログエントリにファイル内容そのものを保存してbaseを正しく復元する仕組み（spec 3.4節）、(2) 真の競合のみを`pending_conflicts.json`にローカル記録し、Settings画面の「Resolve Conflicts...」ボタンから解決するGUIフロー（spec 3.3.2節）を実装した。`internal/merge/cli.go`は削除済み。

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

## Phase 13：GUI Settings画面・未設定時自動プロンプト・設定初期化機能

対応する仕様書セクション：3.5.2節（GUI UI仕様）

- [x] 未設定時（`config.json`未存在・`VaultPath`未指定）の起動時自動 Settings プロンプト表示ロジック
- [x] 設定済みの場合のサイレント常駐・バックグラウンド同期開始ロジック
- [x] メニューバーからの `Settings...` 画面表示（Vaultパス選択ダイアログ、rclone設定、同期間隔入力）
- [x] `Save & Start Sync` ボタンによる設定保存および自動 `init` 実行
- [x] `Reset Configuration` ボタンによる設定クリア・初期化機能（確認ダイアログ付き）
- [x] **コンパイル確認**：`go build ./...` → `go test ./...` を実行し、全パッケージでエラー・テスト失敗を解消する

---

---

## Phase 14〜19：Google Drive中心アーキテクチャへの移行（2026-08-27〜検討開始）

**背景：** iCloud Drive上に置いたVaultへObsidianが直接書き込む現行方式は、iCloudの内部デーモン（bounced copy等の独自コンフリクト処理）とこのアプリのスキャン・マージ処理が同じファイルを同時に触ってしまうリスクを構造的に抱えている（詳細はunitevault-spec.md 3.6.1.6節「アーキテクチャ移行の経緯」を参照）。これを解消するため、Vault本体はローカル専用フォルダに置き、Google Drive（rclone）を全PC間の主同期経路とし、iCloudは「iPhoneとの橋渡し専用のステージング領域」に限定する方式へ移行する。

対応する仕様書セクション：1.3節・1.4節・2章・3.5節・3.6節（全面改訂、詳細は仕様書側に記載）

### Phase 14：設定・データモデルの拡張

- [ ] Vaultのデフォルト置き場所をローカル専用フォルダに変更（Mac: `~/ObsidianVault`、Windows: `%USERPROFILE%\ObsidianVault`）。iCloud配下に置く運用は禁止しない（自己責任で可）が、推奨・デフォルトではなくする
- [x] `config.Config`に新フィールド追加：`ICloudBridgePath`（任意。空文字列＝iPhone連携なし・Google Drive同期のみ）
- [ ] `config.Config`に新フィールド追加：`TickIntervalSeconds`（デフォルト60秒）。既存の`IntervalSeconds`は「Google Drive同期・iCloud Bridge同期それぞれの実効間隔」の目安表示用に意味を変更するか、廃止して`TickIntervalSeconds`に一本化するかを実装時に決定する
- [ ] iCloud Bridge用の仮想デバイスIDの生成・永続化（`icloud_bridge_device_id`、本体の`device_id`とは別に1つ、Primary機のみが持つ）— Phase 15（継続的なブリッジ同期）着手時に実施。現時点のVault Migration機能は初回シードコピーのみで、継続的な仮想デバイスとしての扱いはまだ無い
- [x] **Vault Migration機能を先行実装**（Settings画面の「Migrate Vault to Local Folder...」ボタン）：既存Vaultフォルダの選択（OS標準ダイアログ）→ ローカル標準フォルダへの移動（`bootstrap.MoveVaultFolder`、同一ボリューム内はrename、クロスボリュームはcopy+delete）→ Obsidian自身のvault一覧（`obsidian.json`）のベストエフォート更新（`internal/obsidianconfig`、更新前にバックアップ作成）→ iCloud Driveが検出できればブリッジフォルダへの初回シードコピー（`bootstrap.ICloudDriveRoot`/`CopyDirRecursive`）→ 既存の`saveSettingsConfirmed`（remote未設定なら設定を促す・`InitializeNode`・デーモンループ起動）へ引き継ぎ、という一連の流れとして実装済み。**ただし、iCloud Bridgeは今のところ「移行時の初回シードコピー」のみ**で、その後の継続的な双方向同期・マージへの組み込みは未実装（Phase 15で行う）
- [ ] **コンパイル確認**：実施済み（Vault Migration機能の追加分について。Phase 14の残り項目は引き続き未着手）

### Phase 15：iCloud Bridge機構の実装（継続的な双方向同期。実装済み）

**実装済み。** `internal/engine/bridge.go`に、Vault本体とは独立したブリッジフォルダのスキャン・仮想デバイスとしてのログ記録・マージ結果の書き戻しを実装した。`RunCycle`から、Primaryかつ`ICloudBridgePath`が設定されている場合にのみ呼び出す（ベストエフォート、失敗してもGoogle Drive同期は継続する）。

- [x] ブリッジフォルダ（`ICloudBridgePath`）を対象にした独立スキャン（`internal/scan.Scanner`を別ルートパスに対して再利用）
- [x] ブリッジ用ログ（Vault本体の`_sync/log-<bridge-id>.jsonl`）への変更記録 - 仮想デバイスとして通常のデバイスログと同じスキーマに乗せる（`ScanBridgeAndLog`）。仮想デバイスIDは`config.ConfigManager.GetOrCreateBridgeDeviceID`で生成・永続化（`icloud_bridge_device_id`）
- [x] 既存の`mergeAndTrackConflicts`にブリッジデバイスがそのまま`devEntries`の一員として混じることを確認 - 追加のマージロジック変更は不要だった
- [x] マージ結果をブリッジフォルダへ書き戻す処理（`MirrorVaultToBridge`。`os`パッケージでの素朴な再帰コピー＋差分のあるファイルのみ書き込み＋Vaultから消えたファイルの削除、という手作りのローカル⇔ローカルミラー）
- [x] ブリッジフォルダが存在しない／未設定の場合のフォールバック（`ICloudBridgePath`が空ならスキャン・書き戻しとも単純にスキップ）
- [x] 単体テスト（`internal/engine/bridge_test.go`：ブリッジ経由の変更が本体Vaultへ正しく反映されること、マージ結果の書き戻し、ブリッジ自身の`_sync/`が誤って上書きされないこと）
- [x] **コンパイル確認・フルテスト**

**重要：この実装の過程で、`internal/scan`の変更検出パイプライン自体に、Phase 15固有ではない、より根本的なバグを発見・修正した（spec 3.4.2節）。** 既存ファイルの編集が、デバウンス方式と組み合わせると正しくログに記録されず（偽の削除が1回記録された後、本来のmodify/createが永久にログに残らない）、これは3-way merge（3.3節）が依存するログ自体の正しさを損なう、Phase 15より深刻な問題だった。`_sync/state/last_scan.json`（デバウンス比較専用）と`_sync/state/last_confirmed_scan.json`（変更検出の比較基準、新設）を分離し、さらに「デバウンス未確定」と「本当の削除」を区別する`ReconcileForDetection`を追加することで解決した。`internal/scan/scan_test.go`に、実際の複数サイクルを通しで検証する回帰テストを追加済み。

### Phase 16：Google Drive同期をSecondaryにも拡張（エンジン部分は実装済み）

- [ ] Secondary機もGoogle Driveリモートの設定を必須にする（Settings画面の案内・バリデーションを更新）- **未着手**。現状はリモート未設定のSecondaryはDrive関連処理を静かにスキップするだけ（エラーにはならないが、Google Drive経由の反映もされない）
- [x] Secondary: 自分の`_sync/`（ログ）のみをGoogle Driveへ`rclone copy`でアップロード（`sync`ではなく`copy`を使い、リモート側の削除・他デバイス分ファイルの巻き添え削除を防ぐ）。Vault現物ファイルはpushしない - ログエントリの`diff`フィールドに全文が入っている（3.4節）ため、Primary側のマージにはログだけで十分
- [x] Primary: Google Driveから全デバイスの`_sync/`（ログのみ、Vault現物は対象外）をpull（`rclone copy`）→ 既存のマージ処理 → マージ後のVault全体を`rclone sync`でGoogle Driveへpublish（Primaryのみがミラー転送の権限を持つ、という既存方針を維持）。`_sync/`のみに限定することで、Primary自身の編集中のVaultファイルがpullで上書きされるリスクを避けている
- [x] Secondary側の`rclone copy` pull処理（Primaryが`sync`で公開した最新のマージ結果、Vault全体を取得しローカルVaultへ反映）
- [x] 単体テスト（`internal/engine/engine_test.go`：Secondaryのpush/pull呼び出し内容、リモート未設定時は何もしないこと、Primaryが`_sync/`のみpullしてから既存のpublishを行うこと）
- [x] **コンパイル確認・フルテスト**

**追記：削除の伝播（マニフェスト方式）を実装済み。** `internal/engine/manifest.go`に`PublishManifest`（Primaryがpublish直前に自分のVaultをスキャンし`_sync/vault_manifest.json`として公開）・`LoadManifest`・`ApplyManifestDeletions`（Secondaryがpull後、自分の確定済みスキャン状態とマニフェストを突き合わせ、確定済みなのにマニフェストに無いファイルだけを安全に削除）を実装。単体テストは`internal/engine/manifest_test.go`。

**残る既知の制約（v1）：** 直近の未確定（デバウンス未安定）なローカル編集が、稀にpullによって上書きされる可能性がある（Obsidianが実際に編集中のVaultフォルダに対して破壊的な`sync`は使わない、という安全側の判断による）。マニフェスト方式も、極端に速い削除と極端に遅い往復が重なるような稀なケースまでは保証しない（ヒューリスティック）。複数Secondaryが同時にpushした場合の競合解消は、Primary側の固定ハブ方式によるマージで扱われる想定だが、実機での検証はまだ行っていない。

### Phase 17：交互スケジューリングの実装

- [ ] 60秒ティックの共通デーモンループ：毎ティックでローカルスキャン・ログ記録・（Primaryのみ）マージを実行
- [ ] 外部同期タスク（Google Drive同期・iCloud Bridge同期）をリストとして持ち、ティックごとに順番に1つずつ実行するラウンドロビン方式（Google Driveが未設定 or iCloud Bridgeが未設定の場合はそのタスクをリストから除外する）
- [ ] 各外部同期の失敗時リトライ方針を3.5.1節の指数バックオフ方針に合わせて見直す
- [ ] Settings画面の「Sync Interval」表示・設定項目を新モデルに合わせて更新（表記・デフォルト値の見直し）
- [ ] **コンパイル確認**

### Phase 18：既存ユーザーの移行

- [ ] 起動時、現在のVaultパスがiCloud Drive配下にあると検出した場合の案内ダイアログ（新方式への移行を推奨する旨、手動での移行手順、または自動移行オプション）
- [ ] 自動移行オプション：新しいローカルVaultフォルダを作成し、既存内容をコピーし、旧iCloud上のVaultフォルダをiCloud Bridge用フォルダとして設定する一連の処理
- [ ] 移行後、旧`_sync/`ログとの整合性確認（device_id・ログファイルの扱いを実装時に精査）
- [ ] **コンパイル確認**

### Phase 19：ドキュメント・テスト・仕上げ

- [ ] `unitevault-spec.md`の全面改訂（本Phaseの各項目に対応する節を実装内容に合わせて最終確認・更新）
- [ ] `README.md`の全面改訂（セットアップ手順・トラブルシューティングを新方式に合わせる）
- [ ] 新機構全体の統合テスト（Google Drive同期のみ／iCloud Bridgeのみ／両方、の3パターン）
- [ ] Windows/Macでの実機検証
- [ ] **コンパイル確認・フルテスト**：`go build ./...` → `go vet ./...` → `go test ./...`、Windowsクロスコンパイル確認

### 将来のTodo（このPhase群のスコープ外）

- Vaultのデータ量・ファイル数に応じて、Google Drive同期・iCloud Bridge同期それぞれの間隔を自動調整、またはユーザーへ変更を提案する機能

---

## 進め方の補足

- 各PhaseはPhase番号の順に進めることを推奨する（Phase 4〈drive〉→Phase 5〈bootstrap〉→Phase 6〈merge〉の順は依存関係上この並びが自然）。
- ただし Phase 2（scan）・Phase 3（syncedlog）は互いに依存が薄いため、順番を入れ替えても問題ない。
- 「コンパイル確認」のステップは省略しないこと。小さい単位でコンパイルを通し続けることで、どのPhaseでエラーが混入したかを常に特定しやすくする。
