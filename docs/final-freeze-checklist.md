# 最終凍結と再起動ドリル（2026-10-31）

実行は 03 のみ。16:45 以降は新規変更なし。18:00 以降は接続も操作もしない。

## 最終結果（17:24 確定）

- **最終走 #33877 POST-PRELOAD-FINAL2: 100096 / messages 1（既知の `GET /api/notifications` 401）、source `8d032135`、binary `17cba64c`。** 以後ベンチは投入しない。
- 経緯: #33193 86930 → 03 が AUTH-RETURNING-1（76394）を投入 → 8d032135 へ戻して #33690 70751（ストア PUT の broken pipe で 500 が 1 件）→ #33877 100096。PUT リトライ版 `91450b3f` は未配備（最終走の後なので配備しない）。

## 以前の記録（17:17）

- **最終走 #33193: 86930 / messages 0、source `8d032135`（554b23a7 + 初期投稿画像 400 件の初期化後再読込）、binary `17cba64c`。** 以後ベンチは投入しない（最終は最後の走）。
- 以後の作業は envcheck 4 台と read-only 確認のみ。17:50 以降無操作、18:00 以降接続禁止。

## 最終判断（17:02、#decisions、shm_11c3）

- 17:02 の「修正を入れない」は **17:13 に本人が「準拠版（約76k）」へ変更**。最終 binary はキャッシュ初期化修正を含む準拠系（`554b23a7` = 3783d99 + profile preload + PGO、#31844 76292 / msg 0）、T68c 配置、`ISUREAL_CACHE_RESET_PEERS` 8 URL。
- 最終手順: 準拠 binary へ戻す → 8 URL reset 204 を確認 → 17:25 頃に最終走（FAIL なら同構成で再走、17:45 開始期限）→ envcheck 4 台 → 17:50 以降無操作。

## 規約対応の経緯（16:50、不採用）

- マニュアル 413–415 / 446–447 の「独自キャッシュも初期化に含める」に対応: `7cff0a53` は /initialize で全 8 プロセスの画像/署名/検索/通知/ip_ban キャッシュを fan-out で初期化（`ISUREAL_CACHE_RESET_PEERS` に 8 URL）。#30556 75933 / msg 0。
- `1e15fa8`（T-0079）はさらに初期化直後に初期データのプロフィール画像と署名を background で再読込（前走のデータは使わない）。
- これにより「ウォームアップ走」は不要（毎走 initialize で同じ状態になる）。以前の 97〜108k は初期化要件を満たさない状態の値で、比較基準にしない。

## 直前の PASS 版（戻し先）

- `89496093`（T68c）: T74 binary + T-0024 LRU + 配置「asia POST → asia2 `isureal-go-asia-post` :8086、europe 書込 → asia1 `isureal-go-europe` :8081、america 書込 → asia2 :8082」+ primary `max_connections=300`。#27198 108525 / messages 1。
- 直前: `690c937d`（T68b、europe 書込が asia2）#26712 97102 / messages 17。`ff08f40f`（T68、asia POST が america1）#26426 74647 / messages 0。
- 配置だけ戻す場合: asia1 nginx を `etc-asia1/nginx/isureal.conf`（POST ローカル）に差し替えて reload、asia2 `isureal-go-asia-post` を `disable --now`。

## T-0075（TCP 設定）は不採用で撤去

- #28631 79945 / messages 1 で効果なし。ドリル前に sysctl=1、initcwnd 除去、unit 削除で元へ戻した。

## binary の識別

- `89496093` の純 Go ビルド = sha256 先頭 `8e0ec1b2`（`ldd` に libcrypto なし）。これを最終にする。
- `9822332d` は OpenSSL（cgo）版 HMAC 試験ビルド。**使わない。**

## 凍結時点の構成（16:50 に確定）

- asia1: nginx(`etc-asia1/nginx/isureal-asia-post-asia2.conf`、POST /api/posts は asia2:8086) + `isureal-go`(:8080, asia) + `isureal-go-europe`(:8081, europe 書込) + guard
- asia2: primary PostgreSQL + `isureal-go-america`(:8082, image store) + `isureal-go-asia-post`(:8086, asia 投稿)
- europe1（nginx は `isureal-europe-writes-asia1.conf`）/ america1: standby PostgreSQL + `isureal-go`(:8080, GET=local standby) + `isureal-go-post`(:8084)
- primary: `synchronous_standby_names = FIRST 2 (europe1, america1)`, `remote_apply`
- 各 Go unit の drop-in `90-eaa-cache-stats.conf` は `/etc/isureal-pprof/<unit>.env`（`ISUREAL_PPROF=127.0.0.1:606x`、ローカル pprof ポートのみ）を読む。挙動に影響なし
- 無効のまま: asia1 `isureal-go-asia-reads`、asia2 `isureal-go-europe`、america1 `isureal-go-asia-offload` / `isureal-go-asia-post`、asia1 の残骸 PostgreSQL（`start.conf=manual`）

## 再起動ドリル結果（16:46 実施・合格）

4 台 + ルーター同時 reboot 後、全 unit 自動復帰、standby 2 台 streaming/sync、envcheck 4 台パス、image store 1133 ファイル、DB 件数一致、ルーター 200/401、4 台から 192.168.10.10:8080/443 → 000、OOM 0、avail 3.1GB 以上。

## 再起動ドリル手順（再実施が必要な場合）

1. 最終版を deploy し、1 走 PASS を確認してから着手。
2. 4 台 + ルーターを AWS API で同時 reboot（`i-00b0826900879795e i-05de0272691b934b0 i-0af3fb3b61a0bd419 i-0210419da00423cc9 i-064eab2e491223cd0`）。
3. 復帰後に確認: 各 host の上記 unit が active、`pg_stat_replication` が europe1/america1 とも `streaming` / `sync`、asia2 の image store ディレクトリが残存、`sudo /usr/local/bin/envcheck` 4 台パス、ルーター経由で `/` が 200。
4. 再起動前に DB の件数（posts/image_objects/comments/realmojis、boundary 以降）と image store のファイル数を控え、復帰後に一致を確認。
5. 最終ベンチ（17:35 頃、遅くとも 17:45 開始）。結果が FAIL なら同じ版のまま再走。
6. 初期化がキャッシュを毎回同じ状態にするため、追試 1 走目との差は再起動直後の OS/PG キャッシュ分のみ。

## 戻し先

- アプリ: `git checkout <直前のPASS版>` → `scripts/deploy.sh`
- 画像ストア無効化: 全 host の `env.sh` から `ISUREAL_IMAGE_STORE_*` / `ISUREAL_IMAGE_CACHE_PEER_URL` を除去して restart（`docs/image-store-runbook.md`）
- レプリカ: `docs/regional-replica-runbook.md`
