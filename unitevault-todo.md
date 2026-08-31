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
- [x] 前回スキャン結果（`.sync/state/last_scan.json`）との比較ロジック
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
- [x] 6.3節の対策：プライマリはローカルVault内`.sync/`にも`PRIMARY_MARKER.json`の複製を保存する処理
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

- [ ] Vaultのデフォルト置き場所をローカル専用フォルダに変更（Mac: `~/Obsidian/Vault`、Windows: `%USERPROFILE%\Obsidian\Vault`）。iCloud配下に置く運用は禁止しない（自己責任で可）が、推奨・デフォルトではなくする
- [x] `config.Config`に新フィールド追加：`ICloudBridgePath`（任意。空文字列＝iPhone連携なし・Google Drive同期のみ）
- [ ] `config.Config`に新フィールド追加：`TickIntervalSeconds`（デフォルト60秒）。既存の`IntervalSeconds`は「Google Drive同期・iCloud Bridge同期それぞれの実効間隔」の目安表示用に意味を変更するか、廃止して`TickIntervalSeconds`に一本化するかを実装時に決定する
- [ ] iCloud Bridge用の仮想デバイスIDの生成・永続化（`icloud_bridge_device_id`、本体の`device_id`とは別に1つ、Primary機のみが持つ）— Phase 15（継続的なブリッジ同期）着手時に実施。現時点のVault Migration機能は初回シードコピーのみで、継続的な仮想デバイスとしての扱いはまだ無い
- [x] **Vault Migration機能を先行実装**（Settings画面の「Migrate Vault to Local Folder...」ボタン）：既存Vaultフォルダの選択（OS標準ダイアログ）→ ローカル標準フォルダへの移動（`bootstrap.MoveVaultFolder`、同一ボリューム内はrename、クロスボリュームはcopy+delete）→ Obsidian自身のvault一覧（`obsidian.json`）のベストエフォート更新（`internal/obsidianconfig`、更新前にバックアップ作成）→ iCloud Driveが検出できればブリッジフォルダへの初回シードコピー（`bootstrap.ICloudDriveRoot`/`CopyDirRecursive`）→ 既存の`saveSettingsConfirmed`（remote未設定なら設定を促す・`InitializeNode`・デーモンループ起動）へ引き継ぎ、という一連の流れとして実装済み。**ただし、iCloud Bridgeは今のところ「移行時の初回シードコピー」のみ**で、その後の継続的な双方向同期・マージへの組み込みは未実装（Phase 15で行う）
- [ ] **コンパイル確認**：実施済み（Vault Migration機能の追加分について。Phase 14の残り項目は引き続き未着手）

### Phase 15：iCloud Bridge機構の実装（継続的な双方向同期。実装済み）

**実装済み。** `internal/engine/bridge.go`に、Vault本体とは独立したブリッジフォルダのスキャン・仮想デバイスとしてのログ記録・マージ結果の書き戻しを実装した。`RunCycle`から、Primaryかつ`ICloudBridgePath`が設定されている場合にのみ呼び出す（ベストエフォート、失敗してもGoogle Drive同期は継続する）。

- [x] ブリッジフォルダ（`ICloudBridgePath`）を対象にした独立スキャン（`internal/scan.Scanner`を別ルートパスに対して再利用）
- [x] ブリッジ用ログ（Vault本体の`.sync/log-<bridge-id>.jsonl`）への変更記録 - 仮想デバイスとして通常のデバイスログと同じスキーマに乗せる（`ScanBridgeAndLog`）。仮想デバイスIDは`config.ConfigManager.GetOrCreateBridgeDeviceID`で生成・永続化（`icloud_bridge_device_id`）
- [x] 既存の`mergeAndTrackConflicts`にブリッジデバイスがそのまま`devEntries`の一員として混じることを確認 - 追加のマージロジック変更は不要だった
- [x] マージ結果をブリッジフォルダへ書き戻す処理（`MirrorVaultToBridge`。`os`パッケージでの素朴な再帰コピー＋差分のあるファイルのみ書き込み＋Vaultから消えたファイルの削除、という手作りのローカル⇔ローカルミラー）
- [x] ブリッジフォルダが存在しない／未設定の場合のフォールバック（`ICloudBridgePath`が空ならスキャン・書き戻しとも単純にスキップ）
- [x] 単体テスト（`internal/engine/bridge_test.go`：ブリッジ経由の変更が本体Vaultへ正しく反映されること、マージ結果の書き戻し、ブリッジ自身の`.sync/`が誤って上書きされないこと）
- [x] **コンパイル確認・フルテスト**

**重要：この実装の過程で、`internal/scan`の変更検出パイプライン自体に、Phase 15固有ではない、より根本的なバグを発見・修正した（spec 3.4.2節）。** 既存ファイルの編集が、デバウンス方式と組み合わせると正しくログに記録されず（偽の削除が1回記録された後、本来のmodify/createが永久にログに残らない）、これは3-way merge（3.3節）が依存するログ自体の正しさを損なう、Phase 15より深刻な問題だった。`.sync/state/last_scan.json`（デバウンス比較専用）と`.sync/state/last_confirmed_scan.json`（変更検出の比較基準、新設）を分離し、さらに「デバウンス未確定」と「本当の削除」を区別する`ReconcileForDetection`を追加することで解決した。`internal/scan/scan_test.go`に、実際の複数サイクルを通しで検証する回帰テストを追加済み。

### Phase 16：Google Drive同期をSecondaryにも拡張（実装済み）

- [x] Secondary機もGoogle Driveリモートの設定を必須にする（Settings画面の案内・バリデーションを更新）。**調査の結果、GUIのSave Settingsフロー（`saveSettingsConfirmed`）は既にリモート未設定では保存できない作りだった**（`driveClient.IsRemoteConfigured`のチェックが役割に関わらず常に実行される）。実際に到達可能だったギャップは別にあった：「Remove Remote Configuration...」（`removeRemote`）がrclone側のリモートは削除するのに`config.json`の`RcloneRemote`/`RclonePath`はクリアしていなかったため、削除後は「存在しないリモート名がconfig.jsonに残ったまま」という壊れた状態になり、次回以降の同期サイクルが（静かにスキップされるのではなく）毎回エラーになっていた。これを修正し、リモート削除時に`config.json`側も一緒にクリアするようにした（正常な「リモート未設定」状態へ）。加えて、Settings画面に「⚠ Google Drive not configured:」の警告行を追加：`DeviceRole == "secondary"`かつ`RcloneConfigured == false`の場合のみ表示し、「Configure Google Drive Remote...」ボタン（rcloneセクションの既存ボタンと同じハンドラ）で即座に設定できるようにした。Secondaryにとって未検出のまま放置されると「他のPCの変更を一切受け取れない」という一番目立ちにくい失敗モードになるため（1.6.4節の通り、Secondaryにとって唯一の受信経路がGoogle Drive）。単体テストは`internal/gui/settings_window_test.go`の`TestBuildSettingsContent_SecondaryDriveWarning`（Secondary＋未設定の組み合わせでのみ表示されること）・`TestBuildSettingsContent_SecondaryDriveWarning_ButtonInvokesConfigureRemote`。
- [x] Secondary: 自分の`.sync/`（ログ）のみをGoogle Driveへ`rclone copy`でアップロード（`sync`ではなく`copy`を使い、リモート側の削除・他デバイス分ファイルの巻き添え削除を防ぐ）。Vault現物ファイルはpushしない - ログエントリの`diff`フィールドに全文が入っている（3.4節）ため、Primary側のマージにはログだけで十分
- [x] Primary: Google Driveから全デバイスの`.sync/`（ログのみ、Vault現物は対象外）をpull（`rclone copy`）→ 既存のマージ処理 → マージ後のVault全体を`rclone sync`でGoogle Driveへpublish（Primaryのみがミラー転送の権限を持つ、という既存方針を維持）。`.sync/`のみに限定することで、Primary自身の編集中のVaultファイルがpullで上書きされるリスクを避けている
- [x] Secondary側の`rclone copy` pull処理（Primaryが`sync`で公開した最新のマージ結果、Vault全体を取得しローカルVaultへ反映）
- [x] 単体テスト（`internal/engine/engine_test.go`：Secondaryのpush/pull呼び出し内容、リモート未設定時は何もしないこと、Primaryが`.sync/`のみpullしてから既存のpublishを行うこと）
- [x] **コンパイル確認・フルテスト**

**追記：削除の伝播（マニフェスト方式）を実装済み。** `internal/engine/manifest.go`に`PublishManifest`（Primaryがpublish直前に自分のVaultをスキャンし`.sync/vault_manifest.json`として公開）・`LoadManifest`・`ApplyManifestDeletions`（Secondaryがpull後、自分の確定済みスキャン状態とマニフェストを突き合わせ、確定済みなのにマニフェストに無いファイルだけを安全に削除）を実装。単体テストは`internal/engine/manifest_test.go`。

**残る既知の制約（v1）：** 直近の未確定（デバウンス未安定）なローカル編集が、稀にpullによって上書きされる可能性がある（Obsidianが実際に編集中のVaultフォルダに対して破壊的な`sync`は使わない、という安全側の判断による）。マニフェスト方式も、極端に速い削除と極端に遅い往復が重なるような稀なケースまでは保証しない（ヒューリスティック）。複数Secondaryが同時にpushした場合の競合解消は、Primary側の固定ハブ方式によるマージで扱われる想定だが、実機での検証はまだ行っていない。

### Phase 17：交互スケジューリングの実装（実装済み）

- [x] 60秒ティックの共通デーモンループ：`config.DefaultIntervalSeconds`を600→60に変更。毎ティック、ローカルスキャン・ログ記録・（Primaryのみ）マージは常に実行する（変更なし、元から毎回実行）
- [x] 外部同期タスク（Google Drive同期・iCloud Bridge同期）をリストとして持ち、ティックごとに順番に1つずつ実行するラウンドロビン方式（`internal/engine/engine.go`の`primaryExternalTasks`・`SyncEngine.tickIndex`）。Google Driveが未設定 or iCloud Bridgeが未設定の場合はそのタスクをリストから除外し、設定されているのが1つだけなら毎ティックそれが実行される（元の動作と同じ）。Secondary側はもともと外部同期先がGoogle Driveの1つだけ（Bridgeを持つのはPrimaryのみ）なので、変更なし。
- [x] 各外部同期の失敗時リトライ方針：`internal/drive.Client.executeWithRetry`が既に3.5.1節の指数バックオフ（30秒→2分→10分）をGoogle Driveの`Sync`/`Copy`双方に適用済みであることを確認。iCloud Bridgeはローカルファイルシステム操作（ネットワークを介さない）のためリトライ対象外のままとする。
- [x] Settings画面の「Sync Interval」表示・設定項目を新モデルに合わせて更新：デフォルト値を600→60に変更（`internal/gui/settings_window.go`の2箇所のフォールバック値）。両方設定時は交互実行のため実効間隔がおよそ2倍になる旨のヒント文言を追加（`internal/gui/translations/ja.json`に日本語訳も追加）。
- [x] **コンパイル確認・フルテスト**：`go build ./...` → `go vet ./...` → `go test ./...`、Windowsクロスコンパイル確認、いずれも成功。単体テストは`internal/engine/engine_test.go`の`TestSyncEngine_RunCycle_Primary_AlternatesExternalSyncTasks`（3サイクル通してDrive→Bridge→Driveと交互に実行されること）・`TestSyncEngine_RunCycle_Primary_SingleExternalTaskRunsEveryTick`（1つしか設定されていない場合は毎ティック実行されること）を追加。

**OS標準のファイル監視（実装済み。上記の交互スケジューリング本体とは別の依頼として先行実装）：** 常時起動のトレイ/メニューバー・プロセスに変わったことを踏まえ（元々cronベースだった頃はOS監視を採用しない理由になっていた前提が変わった、3.3.0節参照）、`internal/watch`パッケージを新設。`fsnotify`（Fyne経由で既に間接依存として存在、v1.9.0）をラップし、Vaultフォルダ（`.sync/`は除外）を再帰的に監視、新規サブディレクトリも動的に監視対象へ追加する。あくまで「フルスキャンを置き換えない、ベストエフォートのヒント」という位置付け（クラウド同期フォルダ、特にiCloud Bridgeでは監視イベントの取りこぼしが起こり得るため、Bridge側は引き続きポーリング/フルスキャン方式のまま、`Watcher`には接続していない）。

- `internal/watch/watch.go`：`Watcher`型。`New(root)`で監視開始、`Drain()`で前回`Drain`以降に変化のあった相対パス一覧を取得・クリア、`Close()`で終了。
- `internal/scan/scan.go`：`Scanner.ScanPaths(baseline, paths)`を追加。baselineを引き継ぎつつ、指定パスだけ再スキャンする軽量版`ScanVault()`。
- `internal/engine/engine.go`：`SyncEngine.SetWatcher(w)`（オプションのセッター。`RunCycle`のシグネチャ自体は変更せず、既存の呼び出し箇所・テストへの影響を回避）。`scanStep`内部メソッドで、Watcher未接続時・1サイクル目・30サイクルに1回の定期フルスキャン（`watcherFullScanEvery`定数）の場合はフルスキャン、それ以外はWatcherがdrainしたパスのみを`ScanPaths`で再スキャン、という方針。
- `cmd/unitevault/main.go`：トレイアプリの`runDaemonLoop`、およびCLIの`unitevault run`（`--once`指定時を除く）の両方でWatcherを生成・アタッチし、ループ終了時にクローズ。
- テストは`internal/watch/watch_test.go`（実ファイルシステムに対する非同期イベント検証、ポーリング待ち）、`internal/scan/scan_test.go`（`ScanPaths`単体）、`internal/engine/engine_test.go`（Watcher接続下でも複数サイクルを通した変更検出が正しく動くこと、1サイクル目はWatcher作成前から存在するファイルも見逃さないこと）。

### Phase 18：既存ユーザーの移行（実装済み）

- [x] 起動時、現在のVaultパスがiCloud Drive配下にあると検出した場合の案内ダイアログ（新方式への移行を推奨する旨、手動での移行手順、または自動移行オプション）。`cmd/unitevault/main.go`の`maybeShowICloudMigrationReminder`（`startup()`から、既に設定済みで役割が確定しているデバイスに対してのみ呼ばれる）。検出の中核ロジックは純粋関数`pathIsUnder(root, path)`に切り出し、単体テスト（`TestPathIsUnder`）を追加。「今すぐ移行」「今後表示しない」の2択＋ダイアログ自体のCancel（＝次回起動時にまた聞く）。「今後表示しない」の選択は`config.ConfigManager`の`ICloudMigrationReminderDismissedPath`／`IsICloudMigrationReminderDismissed`／`SetICloudMigrationReminderDismissed`（`IsInstallReminderDismissed`と全く同じパターン）で永続化し、`ResetConfig`でもクリアされる。
- [x] 自動移行オプション：**Phase 14で実装済みの手動「Migrate Vault to Local Folder...」フロー（`runVaultMigration`）をそのまま再利用**する設計にした（フォルダ選択ダイアログだけをスキップし、`oldPath`は検出済みの現在のVaultパスを使う）。もとの計画にあった「旧iCloud上のVaultフォルダ自体をiCloud Bridge用フォルダとして設定する」という案は採用しなかった——手動フローと処理を分岐させると2つの移行経路が別々の挙動になり分かりにくくなるため、手動フローと同じく「新しいローカル場所へ移動し、iCloud Bridge用には`<iCloud Drive>/Obsidian/<フォルダ名>`へ新規シードコピーする」という一貫した挙動にした。
- [x] 移行後、旧`.sync/`ログとの整合性確認：**精査の結果、対応不要と判明した。** `device_id`・`role`・`icloud_bridge_device_id`・保留中コンフリクト・Drive同期ステータス等はすべてVaultとは独立したアプリ設定ディレクトリ（`ConfigManager.configDir`、Vault配下ではない）に保存されている。Vault配下の`.sync/`（各デバイスのログ・スキャン状態・マニフェスト）は`bootstrap.MoveVaultFolder`がVaultフォルダ全体をまるごと移動（同一ボリューム内は`rename`、クロスボリュームは再帰コピー＋削除）するため、`.sync/`だけ取り残される・パスが古いままになる、といった不整合は構造上発生しない。ログエントリやスキャン状態はいずれもVaultルートからの相対パスのみを保持しており、絶対パスへの依存もない。
- [x] **コンパイル確認・フルテスト**：`go build ./...` → `go vet ./...` → `go test ./...`、Windowsクロスコンパイル確認、いずれも成功。

### Phase 19：ドキュメント・テスト・仕上げ（自動テスト・ドキュメントは完了。実機検証のみ残る）

- [x] `unitevault-spec.md`の全面改訂：1〜4章・6章が旧方式（iCloud上にVault・cron実行・Google Driveは片方向バックアップのみ）を現行方式として記述したままだった箇所を洗い出し、システム構成図（2章）・Vault保存場所（3.1節）・実行モデルとOS監視（3.3節・3.3.0節）・実行間隔のデフォルト（3.4.1節）・運用フロー（4章）・ファイル構成（6.1節・6.4節・6.5節）を新方式の内容に書き換えた。1.6節冒頭も「移行中」から「これが現行方式」という表現に変更。
- [x] `README.md`の全面改訂：VaultはローカルフォルダがデフォルトでMigrate Vaultで既存iCloud Vaultから移行する案内、60秒交互ティック、2台目以降のPCは空のVaultで開始し初回同期で自動的に内容が入ることの説明などに更新。
- [x] 新機構全体の統合テスト（`internal/engine/integration_test.go`、Google Drive同期のみ／iCloud Bridge／両方、の3パターン）：実際にファイルシステムを操作する（呼び出し記録のみのモックではない）偽のGoogle Driveリモート（`fsDriveRunner`）を新設し、複数の実`SyncEngine`インスタンス間で実際にコンテンツが収束することを検証。**このテストの作成中に、既存の呼び出し記録型モックでは検出できなかった重大なバグを2件発見・修正した：**
  - `mergeAndTrackConflicts`が「変更したデバイスが1台のみ」のパスを常にスキップしていたため、Secondary（またはiCloud Bridge仮想デバイス）だけが行った変更がPrimaryのVaultへ一切反映されないバグ（`applySingleDeviceChange`で修正。詳細は3.3節参照）。Google Drive中心方式ではこれがSecondaryの変更をPrimaryへ伝える唯一の経路だったため、実害のあるバグだった。
  - `MirrorVaultToBridge`が「Vaultに存在しない」というだけでBridge側のファイルを削除していたため、iPhoneが作ったばかりでまだデバウンス確定していない新規ファイルが、確定前のミラー実行で誤って削除され、二度とVaultへ届かないバグ（ブリッジ自身の確定済みスキャン状態にあるパスのみ削除するよう修正。詳細は1.6.3節参照）。
- [ ] Windows/Macでの実機検証：自動テストではカバーできない、実際のGoogle Driveアカウント・複数の実機・iPhoneを使った検証はまだ行っていない。
- [x] **コンパイル確認・フルテスト**：`go build ./...` → `go vet ./...` → `go test ./...`、Windowsクロスコンパイル確認、いずれも成功。

**追記：ブックキーピング用ディレクトリを`_sync/`から`.sync/`へ改名（実装済み、spec 1.6.9節）。** 実機での動作確認中、Obsidianのファイルエクスプローラーに`_sync`フォルダがそのまま見えてしまい、ユーザーが誤って操作しかねないという指摘を受けて対応。Obsidianは`.obsidian`や`.git`同様、ドット始まりのフォルダをデフォルトで自動的に隠す（ユーザー側で何も設定不要）ため、`internal/syncdir`パッケージで名前を一元管理し、`.sync`へ変更した。旧`_sync/`からの自動移行処理は、この時点でテスト環境のデバイスしか使っていなかった（実運用データが無かった）ため、コード量を抑える判断で実装しなかった（ユーザー指示により、実装済みだった移行コード一式を削除）。

**追記：Vault Migrationの移行先を`~/Obsidian/<フォルダ名>`へ変更、rclone Remote Nameのデフォルトを`ObsidianVault`→`Vault`へ変更（実装済み）。** ユーザー指摘により対応。`bootstrap.MoveVaultFolder`が移行先の親フォルダを`os.MkdirAll`で自動作成するよう修正（親フォルダが無いと同一ボリューム内の移動でも失敗し得たバグの修正込み）。

**追記：iCloud Bridgeフォルダの配置先を修正（実機検証により発見・修正済み、spec 1.6.3節）。** v0.0.52でVault Migration時に一般のiCloud Drive配下の`Obsidian`フォルダへブリッジをシードコピーするよう実装したが、実機検証で**iPhone版Obsidianからは実際に開けない**ことが判明した。iPhone版Obsidianの「iCloudを使う」オプションでVaultを作成すると、Obsidian自身の専用iCloudコンテナ（Macでは`~/Library/Mobile Documents/iCloud~md~obsidian/Documents/<Vault名>`、一般のiCloud Drive＝`com~apple~CloudDocs`とは別系統）に保存されることが分かったため、Mac側のブリッジ配置先をこちらに変更した（`bootstrap.ObsidianICloudContainerRoot`）。Windowsについては、iCloud for Windowsにアプリ専用コンテナへのアクセス手段が無いことをObsidianコミュニティフォーラムで確認済みのため、従来通り一般のiCloud Drive配下の`Obsidian`フォルダのままとした（`bootstrap.ICloudBridgeParentDir`が両OSの分岐を吸収）。既存ユーザーへの移行案内（`maybeShowICloudMigrationReminder`）も、Mac限定でこの専用コンテナ配下も検出対象に追加した。

**追記：Windowsにも同種のObsidian専用iCloudコンテナが実在することが実機検証で判明、`bootstrap.ICloudBridgeParentDir`を廃止し`ObsidianICloudContainerRoot`に一本化（修正済み）。** 直前の追記で「Windowsにはアプリ専用コンテナが無い」としていたのはObsidianコミュニティフォーラムの情報に基づく推測だったが、誤りだった。ユーザーが実機（Windows PC）で`dir`コマンドを実行し、`C:\Users\<ユーザー名>\iCloudDrive\iCloud~md~obsidian`配下にVaultが直接存在することを確認（Mac側と異なり`Documents`という中間階層は無い）。あわせて、既存の`ICloudDriveRoot`のWindows分岐が使っていた`iCloud Drive`（スペースあり、File Explorerの表示名）というフォルダ名も誤りで、実際のフォルダ名は`iCloudDrive`（スペース無し）であることをWeb検索で確認し修正した（support.apple.com/guide/icloud-windows/set-up-icloud-drive-icw0144825a5）。これにより`ObsidianICloudContainerRoot`はMac・Windowsとも同じロジック（`os.Stat`での存在確認のみ）で解決できるようになり、OS分岐用の`ICloudBridgeParentDir`は不要になったため削除した（`maybeShowICloudMigrationReminder`・`runVaultMigration`とも`ObsidianICloudContainerRoot`を直接呼ぶ）。

**追記：Windows自己更新ヘルパーを、コンソールを一切生成しないコンパイル済みGoバイナリへ置き換え（実機での再発報告により修正済み）。** v0.0.49→v0.0.54への実機アップデート時に、ping実行時のコンソールウィンドウが一瞬見えるバグ（過去に修正済みだったはず）が再発したとの報告を受けた。原因は、待機・強制終了に使っていた`ping`/`taskkill`が`cmd.exe`とは別のコンソールサブシステムの実行ファイルであるため、`cmd.exe`自体を`CREATE_NO_WINDOW`で起動していても、環境（Windows Terminalがデフォルトのコンソールホストになっている等）によっては一瞬コンソールが表示され得ることだと推測された。ユーザーから提案された`timeout`コマンドへの切り替えは、コンソールを持たないプロセスから呼ぶと`/nobreak`指定でも「Input redirection is not supported」で即終了してしまう既知の問題があり採用しなかった（Web検索で確認）。代わりに、待機・強制終了・ファイル入れ替え・再起動のロジック全体を新規パッケージ`cmd/unitevault-updatehelper`（コンソールサブシステムを持たない`-H=windowsgui`ビルド）に実装し直し、`internal/selfupdate`へ`go:embed`で埋め込むよう変更した（リリースワークフローに、埋め込み対象を先にビルドするステップを追加）。スワップ・リトライの核心ロジック（`update.go`）はOS非依存の純粋なファイル操作のみで書き、`killProcess`/`startDetached`だけをOS別ファイルに分離したことで、Windows実機が無くてもmacOS上で実際にリトライ・失敗時のロールバック挙動までユニットテストできるようになった（`update_test.go`）。

**追記：Vaultの「移行が必要」判定を、個別サービス検出からホワイトリスト方式へ一般化し、新規Vault選択時にも自動移行を追加（実機での指摘により修正済み）。** iCloud上のVaultを誤ってそのまま指定してしまうと、iCloudの同期デーモンと競合して同期がハングアップする不具合が実機で見つかった。根本対応として、「iCloud Drive配下か」「Obsidian専用iCloudコンテナ配下か」を個別に検出していたブロックリスト方式（`ICloudDriveRoot`/`ObsidianICloudContainerRoot`を都度チェック）をやめ、「`bootstrap.ManagedVaultParentDir`（`~/Obsidian`）配下にあるかどうか」の一点のみを見るホワイトリスト方式に一般化した（`vaultUnderManagedRoot`）。将来Dropbox等の別サービスが増えても検出ロジックの追加が要らなくなる。あわせて、既存ユーザー向けの起動時リマインダーだけでなく、**Settings画面でSelect Folderにより新しくVaultを選んだ場合（初回セットアップ含む）にも、選んだ場所が管理フォルダ外ならSave Settings実行時に自動で移行フローへリダイレクトする**機能を追加した（`vaultNeedsAutoMigration`）。これにより、そもそも管理フォルダ外のVaultを設定できてしまう入り口自体を塞いだ。「Migrate Vault to Local Folder...」ボタンは、rcloneリモート設定済みの間はSelect Folder自体が無効化される仕様のため、運用開始後にVaultを別の場所へ移動する唯一の手段として残している。

**追記：`_sync/`のGoogle Drive上への残存データが再ダウンロードされる不具合を修正（実機報告により発見・修正済み、spec 1.6.9節）。** `_sync`→`.sync`リネーム時、旧名からの移行コードはあえて実装しなかった（実運用データが無かったため）。しかしリネーム前にテストで使っていたGoogle Driveリモートには当時の`_sync/`が消えずに残っており、Vault全体を対象とするrcloneの`copy`/`sync`（フォルダ名の新旧を区別せず「そこにあるものをそのまま」ミラーする）が、これを同じリモートへ後から参加したデバイスへ何度でも再ダウンロードしてしまうことが実機で判明した（コード自体に`_sync`という文字列は残っていなかった）。`internal/syncdir`に`LegacyName = "_sync"`と`IsBookkeeping(slashRel)`（`.sync`・`_sync`どちらの配下も判定）を追加し、Vault全体を対象とするrcloneの除外パターンへ`/_sync/**`を追加、スキャナ・iCloud Bridgeミラーの除外判定も同じ関数に統一した。能動的な削除は行わず（除外により今後は無害に取り残されるだけ）、既に汚染されたリモート・ローカルの`_sync/`自体はユーザーが手動で削除する運用とした。

**追記：直前の`_sync`残存データ対応（`syncdir.LegacyName`/`IsBookkeeping`）を削除（ユーザー指示により）。** 原因となったGoogle Driveリモート・ローカルの`_sync/`をユーザーが手動で削除し、次回テストへ備えた上で、「今後別の未クリーンなリモートを再利用しない限り再発しない」「実運用ユーザーのデータがまだ存在せず、実害も軽微（フォルダが一つ増えるだけ）」という判断から、恒久的な防御コード自体を削除する方針とした（`_sync`→`.sync`リネーム時に移行コードを持たなかったのと同じ判断基準）。万一別のリモートで再発した場合も、原因は既に分かっているため必要になった時点で同じ対応を再実装すればよい。

**追記：Windows日本語UIでの英数字・日本語混在テキストのベースラインずれを修正（実機報告により発見・修正済み、spec 8.5.5節）。** 「未設定 - リモート「Vault」がrcloneに見つかりません」のような文言で、`Vault`/`rclone`部分が日本語部分より下にずれて見える不具合がWindows実機で見つかった（macOSでは目立たない）。原因はFyne標準の欧文フォント（Inter）とOS依存の日本語フォールバックフォントの組み合わせによるベースラインの違い。Noto Sans JP（Regular・Boldの2ウェイト、SIL OFL 1.1）を`internal/gui/fonts/`に埋め込み、`internal/gui/font_theme.go`の`unifiedFontTheme`で全スタイルをこのフォントに統一することで、フォント混在自体をなくして解決した。バイナリサイズは増加するが、ユーザーの了承済み。

**追記：Vault Migrationの2つの改善（実機報告により発見・修正、spec 1.6.7節）。**

1. **`obsidian.json`更新失敗の警告を、本当に想定外の場合だけに限定。** 実機テストで、iPhone側でiCloud経由に作成しただけで一度もMac版Obsidianで開いたことが無いVaultを移行した際、「Obsidian自身のVault一覧を自動更新できませんでした」という警告が出た。調査の結果、`obsidian.json`にそもそも該当Vaultのエントリが存在しない（＝Obsidianがそのvaultを一度も認識していない）という、正常にあり得る状態が原因と判明——バグではないが、この状態を「ファイルが壊れている」等の本当の異常と同じ扱いで警告していたのは不親切だった。`obsidianconfig.UpdateVaultPath`が「一致するエントリが無い」場合は真のエラーとは区別し、警告を出さないよう修正した。
2. **移行元がすでにiCloud Bridgeの配置先だった場合、削除せずコピーのみ行うよう変更。** 従来は移行元がBridge配置先そのものであっても常に「移動」し、直後にBridge用として同じ場所へ「シードコピー」し直していた。これはiCloudの同じフォルダを一瞬空にしてから即座に埋め戻す動作であり、v0.0.52の不具合と同種の危険なパターンだと実機テストで指摘を受けた。移行元がBridge配置先そのものかどうかで分岐し、そうであればiCloud側を一切削除せず「コピーのみ」、そうでなければ従来通り「移動してから新規シード（まだ何も無い場所への新規作成なので安全）」とすることで、iCloud側の同一フォルダを削除・再作成する場面自体を無くした。

**追記：Windowsでの一瞬のコンソールウィンドウ表示を全面的に排除（実機報告により発見・修正済み、spec 8.4節）。** 自己更新ヘルパーのコンソール表示は既に修正済みだったが、実機テストで「同期サイクルのたび（`rclone`呼び出し）」「3-way merge発動のたび（`git merge-file`呼び出し）」にも同様の一瞬の表示が起きていることが判明した。原因は、これらの箇所が`CREATE_NO_WINDOW`を一切設定せずに`exec.Command`でコンソールサブシステムの実行ファイルを起動していたため。新設した共通ヘルパー`internal/winexec.HideWindow`（旧`internal/bootstrap`の非公開`hideWindow`を格上げ・統合したもの）を、`internal/drive`のrclone呼び出し全箇所・`internal/merge`のgit呼び出し・`internal/bootstrap`のtasklist/taskkill/winget/Gitインストーラー/iCloud起動用cmd/rundll32、すべてに適用した。意図的にユーザーへ見せる対話的ウィンドウ（rclone configのPowerShellターミナル、Gitインストーラーのフォールバック起動）は対象外とした。

**追記：Secondaryの編集がpullで消えるデータロスバグと、iCloud Bridgeの読み取りをSecondaryにも拡張（実機報告により発見・修正済み、spec 1.6.3節・1.6.4節）。**

1. **Secondaryの未確定編集が同サイクルのpullで上書きされ消える不具合を修正。** Phase 16時点では「稀な既知の制約」として受け入れていたが、Windows実機で「保存直後に同期サイクルが回ると編集が跡形もなく消える」ことが実際に発生すると報告を受けた。原因は、デバウンスでまだ安定していない（ログ未記録の）編集を、同じサイクルの「Primary公開済み内容の`rclone copy`によるpull」が物理的に上書きしてしまうこと。`scan.UnstablePaths`（今回のスキャンでまだ安定していないパスを返す）を追加し、pullの`--exclude`へ動的に加えることで解消した。デバッグの過程で、`ApplyManifestDeletions`が「自分自身が直前に確定したファイルを、Primaryのマニフェスト公開がまだ追いついていないタイミングで誤って削除してしまう」別のレースが既に存在し、これまでは（今回修正対象の）pullが結果的にその削除を追加コピーで自己修復していたことも判明した。そのため`UnstablePaths`は意図的に「直前のスキャンから消えたパス（削除）」を保護対象に含めない設計とした（削除まで保護すると、この自己修復を妨げて別の恒久的データロスを生んでしまうため）。実際にこの設計判断が無いとテストが壊れることを、統合テストで確認済み。
2. **iCloud Bridgeの読み取り(`ScanBridgeAndLog`)をSecondaryにも拡張。** `config.ICloudBridgePath`はVault Migrationを実行したデバイスごとに個別保存されるため、Secondary機でもiCloudを設定していれば持ち得るが、従来はPrimaryしか読み取っていなかった。iPhoneでの編集や、ユーザーが誤って管理フォルダの代わりにiCloud側を直接開いて編集した内容が、Secondary機では永久に無視される抜け穴だった。書き込み(`MirrorVaultToBridge`)は複数デバイスからの同時書き込みによる競合を避けるため引き続きPrimaryのみとし、読み取りだけを全デバイスに拡張した。

なお、この一連の議論の中で「iCloud中心方式への回帰」「Primary/Secondaryの区別を無くした対称同期（gitのようなCAS方式）」という2つの大きな代替案も検討したが、いずれも実装コスト・信頼性の観点から不採用とした（前者は3.6.1.6節の既知の制約を再導入するリスク、後者はrcloneの単純なファイル操作だけではgitのpush拒否に相当する調整機構を安全に実現できないため）。詳細な検討経緯は本セッションの会話ログを参照。

### 将来のTodo（このPhase群のスコープ外）

- Vaultのデータ量・ファイル数に応じて、Google Drive同期・iCloud Bridge同期それぞれの間隔を自動調整、またはユーザーへ変更を提案する機能

---

## 進め方の補足

- 各PhaseはPhase番号の順に進めることを推奨する（Phase 4〈drive〉→Phase 5〈bootstrap〉→Phase 6〈merge〉の順は依存関係上この並びが自然）。
- ただし Phase 2（scan）・Phase 3（syncedlog）は互いに依存が薄いため、順番を入れ替えても問題ない。
- 「コンパイル確認」のステップは省略しないこと。小さい単位でコンパイルを通し続けることで、どのPhaseでエラーが混入したかを常に特定しやすくする。
