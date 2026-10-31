# 最終凍結と再起動ドリル（2026-10-31）

実行は 03 のみ。16:45 以降は新規変更なし。18:00 以降は接続も操作もしない。

## 凍結時点の構成（16:45 に確定）

- asia1: nginx(`etc-asia1/nginx/isureal.conf`) + `isureal-go`(:8080, asia) + guard
- asia2: primary PostgreSQL + `isureal-go-america`(:8082, image store) + `isureal-go-europe`(:8081)
- europe1 / america1: standby PostgreSQL + nginx(`etc-remote/nginx/*-local-reads.conf`) + `isureal-go`(:8080, GET=local standby) + `isureal-go-post`(:8084)
- primary: `synchronous_standby_names = FIRST 2 (europe1, america1)`, `remote_apply`
- 各 Go unit の drop-in `90-eaa-cache-stats.conf` は `/etc/isureal-pprof/<unit>.env`（`ISUREAL_PPROF=127.0.0.1:606x`、ローカル pprof ポートのみ）を読む。挙動に影響なし
- 無効のまま: asia1 `isureal-go-europe` / `isureal-go-asia-reads`、america1 `isureal-go-asia-offload` / `isureal-go-asia-post`（T-0068 採用時のみ enabled）

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
