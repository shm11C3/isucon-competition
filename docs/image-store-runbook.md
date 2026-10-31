# T-0062 投稿画像ファイルストア（WAL から画像バイトを外す、耐久性維持）

## 構成
- asia2 の isureal-go-america（:8082）が `ISUREAL_IMAGE_STORE_DIR=/home/isucon/image-store` でストアを兼ねる（同一バイナリ、内部 HTTP `/internal/images/:id`、nginx 非公開、トークン必須）。
- 全プロセス（asia1: isureal-go / isureal-go-europe、asia2: america、europe1/america1: isureal-go）に `ISUREAL_IMAGE_STORE_URL=http://192.168.4.10:8082`（asia2 自身は `http://127.0.0.1:8082`）と `ISUREAL_IMAGE_STORE_TOKEN=<共有秘密>`。env.sh に追記すれば全ユニットが読む（guard は無視）。
- 投稿プロセス（europe1/america1 の :8084、asia1 の asia、asia2 の america）には `ISUREAL_IMAGE_CACHE_PEER_URL=http://127.0.0.1:8080` を設定すると、アップロードしたバイトを同一ホストの GET プロセス（:8080）の画像 cache へ内部 PUT（/internal/cache/:id、トークン必須、ベストエフォート）で渡す。GET プロセスは同じトークンで受ける。
- 投稿: 地域プロセスが JPEG 検証後にストアへ PUT（fsync＋rename＋dir fsync 完了で 201）→ create_post_v1 に空 bytea → 自プロセスの画像 cache に投入。失敗時は 500（成功を返さない）。
- 読取: 可視性・署名・期限は従来どおり DB（standby）。bytea が空ならストアから GET（ミスは 403）。export も同様。memories はバイトを読まない。プロフィール画像・RealMoji・初期データは DB のまま。
- initialize: init.sh 後に asia プロセスがストアを全消去（DELETE /internal/images/）。他プロセスの画像 cache は image ID が UUID で再利用されないため残存無害。

## 適用（03）
1. 全 4 host の /home/isucon/env.sh に `ISUREAL_IMAGE_STORE_URL`、`ISUREAL_IMAGE_STORE_TOKEN`、`ISUREAL_IMAGE_CACHE_PEER_URL=http://127.0.0.1:8080`（europe1/america1/asia1。asia2 は GET プロセスが無いので不要）を追記（asia2 は URL を 127.0.0.1:8082、加えて `ISUREAL_IMAGE_STORE_DIR=/home/isucon/image-store`）。`mkdir -p /home/isucon/image-store`（isucon 所有）。
2. deploy.sh で配備（全ユニット再起動）。asia2 の america を先に再起動してから他を再起動するのが安全（ストア未起動中の投稿は 500）。
3. 確認: europe 経由で投稿 → asia2 の image-store にファイル → 画像 GET 200（europe1 cache hit）→ asia 経由で同画像 GET 200（ストア経由）→ export に画像が含まれる → initialize でディレクトリが空。
4. 再起動ドリル: 再起動後にファイルが残り、画像 GET 200。

## 戻し
- env.sh の 3 変数を削除して全ユニット再起動（次の initialize で空 bytea の行は消えるので整合）。ディレクトリは残しても無害。
