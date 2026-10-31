# portal-bench

ポータルのベンチ依頼と結果取得をエージェントから実行するツール (Node + Playwright)。
GitHubログインは人間のみ。パスワード/トークンは扱わない。

## セットアップ
    cd tools/portal-bench && npm install && npx playwright install chromium

## 初回ログイン (人間が1回)
    node portal-bench.mjs login
開いたブラウザでGitHubログイン。完了すると自動終了。セッションは `~/.config/isu-portal/profile` (repo外)。

## コマンド
- `node portal-bench.mjs run [--label TEXT]` : headless。WAITING/RUNNING中は依頼せず終了。依頼後5秒毎に最大5分ポーリングし、JSON1行 `{job,status,pass,score,messages,started,finished,label}` を出力。exit 0=pass, 1=不合格/タイムアウト/実行中, 2=未ログイン・UI不一致
- `node portal-bench.mjs status [job]` : 指定(省略時は最新)ジョブの同JSON。依頼はしない
- UI不一致時は /tmp に screenshot と HTML を保存。セレクタは `portal-bench.mjs` 冒頭の定数を直す

## ルール
ベンチは1人ずつ直列。`run` の前に必ず環境予約を確保すること。対象はチーム自身の環境のみ。
