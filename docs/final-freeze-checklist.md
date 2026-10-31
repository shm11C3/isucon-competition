# 最終凍結と再起動ドリル（2026-10-31）

実行は 03 のみ。16:45 以降は新規変更なし。18:00 以降は接続も操作もしない。

## 直前の PASS 版（戻し先）

- `89496093`（T68c）: T74 binary + T-0024 LRU + 配置「asia POST → asia2 `isureal-go-asia-post` :8086、europe 書込 → asia1 `isureal-go-europe` :8081、america 書込 → asia2 :8082」+ primary `max_connections=300`。#27198 108525 / messages 1。
- 直前: `690c937d`（T68b、europe 書込が asia2）#26712 97102 / messages 17。`ff08f40f`（T68、asia POST が america1）#26426 74647 / messages 0。
- 配置だけ戻す場合: asia1 nginx を `etc-asia1/nginx/isureal.conf`（POST ローカル）に差し替えて reload、asia2 `isureal-go-asia-post` を `disable --now`。

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

## 再起動ドリル（17:30 まで）

1. 最終版を deploy し、1 走 PASS を確認してから着手。
2. 4 台 + ルーターを AWS API で同時 reboot（`i-00b0826900879795e i-05de0272691b934b0 i-0af3fb3b61a0bd419 i-0210419da00423cc9 i-064eab2e491223cd0`）。
3. 復帰後に確認: 各 host の上記 unit が active、`pg_stat_replication` が europe1/america1 とも `streaming` / `sync`、asia2 の image store ディレクトリが残存、`sudo /usr/local/bin/envcheck` 4 台パス、ルーター経由で `/` が 200。
4. 再起動前に DB の件数（posts/image_objects/comments/realmojis、boundary 以降）と image store のファイル数を控え、復帰後に一致を確認。
5. 1 走 PASS。再起動直後は冷えているので、最終スコアの 75% を下回らないかを見る。
6. 最終ベンチ（17:50 までに開始）。結果が FAIL なら直前の PASS 版へ戻して再走。

## 戻し先

- アプリ: `git checkout <直前のPASS版>` → `scripts/deploy.sh`
- 画像ストア無効化: 全 host の `env.sh` から `ISUREAL_IMAGE_STORE_*` / `ISUREAL_IMAGE_CACHE_PEER_URL` を除去して restart（`docs/image-store-runbook.md`）
- レプリカ: `docs/regional-replica-runbook.md`
