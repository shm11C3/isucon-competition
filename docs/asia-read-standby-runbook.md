# 案S: asia1 に hot standby を置き asia の GET をローカル standby へ（Claude-02）

前提: 案R（europe1/america1 standby、remote_apply、GET ローカル）稼働中。asia1 に PG16 と休止データ dir（/var/lib/postgresql/16/main、同一 system identifier）。asia1↔asia2 1.2ms / 190Mbps 超。

## 0. 事前
- EAA03 の枠を取り、basebackup 中（約 12〜15 分）はベンチ停止。asia1 空きメモリ 2.1GB 以上を確認。
- Go: ISUREAL_SYNC_STANDBYS=3 を 5 プロセス全てに渡す（env.sh か drop-in。barrier は sync standby 数が 3 未満なら 503 になる）。**先に設定すると 2 standby 時点で 503 になるので、同期 FIRST 3 に切り替えるのと同時に反映する。**

## 1. standby 作成（asia1）
```
sudo systemctl stop postgresql@16-main 2>/dev/null || true
sudo -u postgres bash -c 'umask 077; printf "192.168.4.10:5432:*:replicator:%s\n" "$PW" > ~/.pgpass'   # PW は asia2 の /var/lib/postgresql/.replicator_pw
sudo -u postgres rm -rf /var/lib/postgresql/16/main && sudo -u postgres mkdir -m 700 /var/lib/postgresql/16/main
sudo -u postgres bash -c 'setsid nohup /usr/lib/postgresql/16/bin/pg_basebackup -h 192.168.4.10 -U replicator -D /var/lib/postgresql/16/main -R -X stream -C -S asia1 --checkpoint=fast -P -v > /var/lib/postgresql/basebackup.log 2>&1 < /dev/null &'
```
- asia2 の pg_hba に `host replication replicator 192.168.1.10/32 scram-sha-256` を追記し reload（europe/america と同じ）。
- 完了後 postgresql.auto.conf の primary_conninfo に `application_name=asia1` を追記（europe1 と同じ手順）。conf.d/zz-standby.conf（本 branch etc-asia1/postgresql/conf.d）を配置。
- `systemctl enable --now postgresql@16-main`、pg_stat_wal_receiver streaming を確認。

## 2. primary を FIRST 3 へ（asia2）
```
ALTER SYSTEM SET synchronous_standby_names = 'FIRST 3 (europe1, america1, asia1)'; SELECT pg_reload_conf();
```
- 同時に 5 Go プロセスへ ISUREAL_SYNC_STANDBYS=3 を配り再起動（barrier の判定）。pg_stat_replication で sync 3 行。

## 3. asia の読取プロセスと nginx（asia1）
- /etc/isureal/local-db.env（ISUREAL_DB_HOST=127.0.0.1）、/etc/systemd/system/isureal-go-asia-reads.service（本 branch）を配置、`systemctl enable --now isureal-go-asia-reads`、`curl 127.0.0.1:8083/api/users?q=a` → 401。
- nginx: 本 branch の etc-asia1/nginx/isureal.conf（GET→:8083、他→:8080）を sites-enabled/isureal.conf へ、`nginx -t && systemctl reload nginx`。
- deploy.sh に `isureal-go-asia-reads` の restart を追加（asia1 の restart 行）。

## 4. 検証
- asia 経由（X-Country-Code: JP）で register→login→GET /api/me 200、PATCH→GET、logout→401。initialize 後に standby で boundary 以降 0 行。
- ベンチ 2 走。asia2 idle/iowait、asia1 idle、GP 完走人数を比較。

## 5. 戻し
- nginx を GET→:8080 へ戻し reload → asia-reads 停止 → primary `synchronous_standby_names='FIRST 2 (europe1, america1)'` reload → Go の ISUREAL_SYNC_STANDBYS を外し再起動 → standby 停止・slot asia1 削除。

## リスク
- asia1 CPU（WAL 適用＋asia クエリ）とメモリ（PG 768MB）。GP で asia1 が先に飽和する可能性。
- 停止点が 3 standby に増える。再起動ドリルのやり直しが必要。
