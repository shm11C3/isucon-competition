# 案R 手順書: europe1/america1 に同期レプリカ、GETをリージョン内で完結（Claude-02, 2026-10-31 11:50 JST）

前提（確認済み）
- europe1/america1: PG16.15 インストール済み・停止中・disabled。/var/lib/postgresql/16/main は asia2 と同じ system identifier 7702429198198691336 の初期スナップショット（checkpoint 4/57A3CDC8、primary は 6/1D02DA40 以降）。
- asia2 の初期画像パーティション（1GB×N）は 10:10 JST 頃に mtime 更新あり（hint bit / VACUUM）。rsync は差分転送になるが、全ページが触られていれば実質17GB転送。先に帯域を計測して判断する。
- GET ハンドラは全て DB 書込なし。POST/PUT/PATCH/DELETE は asia1 の地域別プロセス（:8081 europe / :8082 america、現在 america は asia2:8082）へ転送中。
- isureal-guard は env.sh の ISUREAL_DB_HOST を使う → env.sh は変更せず、isureal-go.service の drop-in で DB ホストを上書きする。
- 実施は Main の環境占有下で、ベンチ測定の合間に行う。asia2 の OOM 復旧完了が前提。

## 0. 事前（5分）
- asia2 メモリ: shared_buffers は 90-isureal.conf の 1536MB が有効。レプリカ化後は asia2 から読み取り負荷が減るので america プロセスの同居は不要になる（asia1 へ戻す or 残すなら imageCache を縮小）。
- 帯域計測（20秒）: asia2 で `python3 -m http.server 8099 --directory /tmp &`、/tmp に 200MB ファイル、europe1 から `curl -o /dev/null -w '%{speed_download}\n' http://192.168.4.10:8099/f` → 終了後 kill。17GB/速度 で所要を見積もる（100Mbps なら約23分/台、並列で asia2 送出が共有なら倍）。
- 所要が30分超なら、初期画像パーティション（base/16385/ 内の 1GB ファイル群で mtime が 10-30 のもの＝europe1 側と同一）を `--size-only` 相当で飛ばす選択肢。hint bit 差は WAL 非記録で standby として有効。ただしスナップショット以後に初期パーティションへ入った WAL 記録変更（プロフィール画像差替えによる旧行 DELETE 等）は再生されない＝stale 行が残り得る。到達経路は users.profile_image_id 経由のみなので実害は小さいが、正しさ優先なら全転送。

## 1. primary (asia2) 準備（5分、再起動不要）
```
sudo -u postgres psql -X <<'SQL'
CREATE ROLE replicator WITH REPLICATION LOGIN PASSWORD '<ランダム>';
SELECT pg_create_physical_replication_slot('europe1', true);
SELECT pg_create_physical_replication_slot('america1', true);
SQL
# pg_hba.conf に追記して reload
echo "host replication replicator 192.168.2.10/32 scram-sha-256
host replication replicator 192.168.3.10/32 scram-sha-256" | sudo tee -a /etc/postgresql/16/main/pg_hba.conf
sudo -u postgres psql -X -c "SELECT pg_reload_conf()"
```
- rsync 用に asia2 の postgres ユーザーで鍵を作り、europe1/america1 の isucon の authorized_keys に追記（既存鍵は消さない）。転送は `--rsync-path='sudo rsync'`。

## 2. ベースバックアップ（rsync 差分、両 standby 並列）
asia2 で psql セッションを開いたまま（pg_backup_start/stop は同一セッション必須）:
```
sudo -u postgres psql -X -v ON_ERROR_STOP=1 <<'SQL'
SELECT pg_backup_start('standby', true);
\! rsync -a --delete --exclude 'pg_wal/*' --exclude 'pg_replslot/*' --exclude 'pg_stat_tmp/*' --exclude 'pg_stat/*' --exclude postmaster.pid --exclude postmaster.opts --exclude backup_label --exclude 'log/*' --rsync-path='sudo rsync' /var/lib/postgresql/16/main/ isucon@192.168.2.10:/var/lib/postgresql/16/main/ &
\! rsync -a --delete （同上を 192.168.3.10 へ）; wait
\o /tmp/backup_label.out
SELECT labelfile FROM pg_backup_stop(true);
\o
SQL
```
- labelfile の中身をそのまま各 standby の `/var/lib/postgresql/16/main/backup_label` に書く（psql の整形を除去し、先頭 `START WAL LOCATION` から）。
- 転送は router 経由（default route が router）。迂回なし。

## 3. standby (europe1/america1) 起動
```
sudo -u postgres bash -c '
cd /var/lib/postgresql/16/main
rm -rf pg_wal/* pg_replslot/* 
touch standby.signal
cat > postgresql.auto.conf <<CONF
primary_conninfo = '"'"'host=192.168.4.10 port=5432 user=replicator password=<同上> application_name=europe1'"'"'
primary_slot_name = '"'"'europe1'"'"'
hot_standby = on
hot_standby_feedback = on
max_standby_streaming_delay = 1s
max_standby_archive_delay = 1s
shared_buffers = 1GB
max_connections = 200
listen_addresses = '"'"'127.0.0.1'"'"'
shared_preload_libraries = '"'"'pg_stat_statements'"'"'
CONF'
sudo systemctl enable --now postgresql@16-main
sudo -u postgres psql -X -c "SELECT status, received_lsn, latest_end_lsn FROM pg_stat_wal_receiver"
```
- america1 は application_name/slot を america1 に。
- /etc/postgresql/16/main/conf.d/isucon.conf の listen_addresses='*' は残っても auto.conf が優先。
- backup_label がある状態で初回起動すると backup start LSN から redo する。「consistent recovery state reached」をログで確認。

## 4. primary を同期モードへ（両 standby が streaming になってから）
```
sudo -u postgres psql -X <<'SQL'
ALTER SYSTEM SET synchronous_standby_names = 'FIRST 2 (europe1, america1)';
ALTER SYSTEM SET synchronous_commit = 'remote_apply';
SELECT pg_reload_conf();
SELECT application_name, state, sync_state, replay_lag FROM pg_stat_replication;
SQL
```
- 両方 sync_state=sync を確認。ここから全 COMMIT が +RTT。

## 5. アプリ経路
europe1/america1:
- `/etc/systemd/system/isureal-go.service.d/local-db.conf`: `[Service]\nEnvironment=ISUREAL_DB_HOST=127.0.0.1`（env.sh は変更しない。guard は primary のまま）→ daemon-reload、restart。
- nginx: GET はローカル :8080、それ以外は現行 upstream（asia1 :8081 / asia2 or asia1 :8082）。
```
map $request_method $isureal_upstream { default isureal_near_db; GET isureal_local; }
upstream isureal_local { server 127.0.0.1:8080; keepalive 64; }
location /api/ { ... proxy_pass http://$isureal_upstream; }
```
- SSE(/api/notifications) は GET → ローカル standby の poller が新 window を見る。remote_apply なので open の応答後には standby にも通知行がある。
- 画像 GET もローカル。bytea cache は不要に近いので上限を縮小（Go 側 const か env）。

## 6. 検証（5分）
1. europe 経由 POST（login→post）直後に europe 経由 GET で見える。asia 経由の書込（friend accept/block）直後に europe 経由 GET で反映。
2. `POST /api/initialize`（asia1）後、standby で `SELECT count(*) FROM users WHERE created_at > boundary` が 0。
3. 公式ベンチ1回。欧米の人数成長と asia の +100ms 書込の影響を見る。
4. 再起動試験: 4台 reboot → standby が自動起動し streaming、primary の COMMIT が通る（standby 未接続中は COMMIT が待つので起動順に注意）。

## 7. フォールバック（2分）
```
sudo -u postgres psql -X -c "ALTER SYSTEM RESET synchronous_standby_names" -c "ALTER SYSTEM RESET synchronous_commit" -c "SELECT pg_reload_conf()"
```
+ europe1/america1 の nginx map を外して全て asia1 転送へ、isureal-go drop-in を外す。standby は止めてよい（slot は削除して WAL 蓄積を止める: `pg_drop_replication_slot`）。

## リスク一覧
- standby 停止/遅延で primary の COMMIT 停止 → 追試の再起動順。slot の WAL 蓄積（33GB 空き）。
- standby 側クエリ競合 → max_standby_streaming_delay=1s で強制キャンセル（GET が稀に 500）。hot_standby_feedback で VACUUM 競合を抑止。
- europe1/america1 メモリ: PG 1GB + Go。image cache 縮小。
- initialize の reset.sql は primary で実行され複製される。standby 側の process cache はなし（visibility は毎回 DB）。

## 実施記録（2026-10-31 12:07 JST 完了）
- 11:52 pg_basebackup（-R -X stream -C -S）を europe1/america1 から asia2 へ、各約17MB/s、12:04 完了。primary_conninfo に application_name を追記。
- 12:05 両 standby streaming、primary を FIRST 2 (europe1, america1) + remote_apply。
- 12:06 isureal-go drop-in（後続 EnvironmentFile=/etc/isureal/local-db.env）と nginx isureal-local-reads.conf を有効化。旧 nginx 設定は sites-available/isureal-forward-all.conf。
- 検証: register→login→GET /api/me 200、PATCH→GET 反映、logout→401、initialize 8.7s、standby 反映 0 行差、conflict 0、write 208ms。
- 戻し: 各 standby で nginx を forward-all に戻し drop-in 削除・isureal-go 再起動 → asia2 で ALTER SYSTEM RESET synchronous_standby_names / synchronous_commit → reload。
