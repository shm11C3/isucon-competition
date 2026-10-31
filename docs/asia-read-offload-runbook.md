# 案F: asia の重い GET（memories / export / 検索）を europe1・america1 の余剰 CPU へ

根拠: #20725 で asia1 は asia プロセス単体で idle 0〜9%（5 日目〜GP）、asia2 も idle 0%、europe1/america1 は idle 50%+。asia プロセス CPU の約 40% は memories の署名 HMAC（初期ユーザー検証 600 ログイン分）。これらは成長に直結しない GET なので +100ms の RTT を払って遠隔ホストの standby で処理する。

## 手順（03）
1. europe1 と america1 に /etc/systemd/system/isureal-go-asia-offload.service（本 branch etc-remote/systemd/system）を配置、`systemctl enable --now`。env は既存 env.sh ＋ local-db.env、REGION=asia、PORT=8085。`curl 127.0.0.1:8085/api/users?q=a` → 401。
2. asia1 nginx の server block に etc-asia1/nginx/asia-read-offload.snippet.conf の内容を追加（upstream/map は server 外、location は server 内の `location /api/` より前）。`nginx -t && reload`。
3. 確認: asia 経由（JP）で login → GET /api/memories?month=2026-06 が 200（europe1/america1 の asia-offload プロセス、journal に get_memories）、GET /api/users?q=a 200、GET /api/me/export 200。feeds/posts は従来どおり asia1。
4. 1 走。asia1 idle と asia の GP 完走数、総点で判定。

## 戻し
asia1 nginx の追加 location/upstream/map を外して reload → 各ホストの isureal-go-asia-offload を disable --now。
## 注意
- PUT/PATCH/DELETE は map で asia1 の :8080 のまま（/api/users は GET のみ対象、/api/me/export は GET）。
- deploy.sh の restart 対象に isureal-go-asia-offload を追加（enabled 時のみ）。

## 変種 案F-all（asia の GET を images/notifications 以外すべて遠隔へ）
- 狙い: asia の読取負荷（feeds・投稿詳細・一覧・memories・検索・export）を asia2 primary と asia1 から外し、×1.5 の欧米に容量を回す。asia の読取は +100ms になり asia の成長は鈍る（容量配分上はむしろ望ましい）。
- asia1 nginx: etc-asia1/nginx/asia-read-offload-all.snippet.conf の upstream/map を server 外、2 つの location で既存 `location /api/` を置換。画像は asia1（cache と store 経路）、SSE は asia1 のまま。
- 必要ユニット・戻しは案F と同じ。
