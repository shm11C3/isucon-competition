# portal-bench

ポータルのベンチ依頼と結果取得をエージェントから実行するツール (Node + Playwright)。
GitHubログインは人間のみ。パスワード/トークンは扱わない。

## セットアップ
    cd tools/portal-bench && npm install && npx playwright install chromium

## 初回ログイン (人間が1回)
    node portal-bench.mjs login
開いたブラウザでGitHubログイン。完了すると自動終了。セッションは `~/.config/isu-portal/profile` (repo外)。

## コマンド
- `node portal-bench.mjs run [--label TEXT] [--operator SESSION] [--source-sha FULL40] [--record PATH]` : headless。WAITING/RUNNING中は依頼せず終了。依頼後5秒毎に最大5分ポーリングし、JSON1行 `{job,status,pass,score,messages,started,finished,label}` を出力。exit 0=pass, 1=不合格/タイムアウト/実行中, 2=未ログイン・UI不一致
- `--operator` と `--source-sha` は実行者/デプロイ元の申告値。source SHAは完全な40桁で検証するが、実際のデプロイ内容とは照合しない。省略時はnullのまま記録する。
- run履歴は既定で `~/.config/isu-portal/benchmark-runs.jsonl` に追記する。`--record PATH` で保存先を変更できる。intent/job_created/result/errorをstderrにも出し、ローカル記録に失敗した場合は警告して処理を続ける。履歴追記失敗を理由にベンチ依頼を再送しない。
- `node portal-bench.mjs status [job]` : 指定(省略時は最新)ジョブの同JSON。依頼はしない
- UI不一致時は /tmp に screenshot と HTML を保存。セレクタは `portal-bench.mjs` 冒頭の定数を直す

## ルール
ベンチは1人ずつ直列。`run` の前に必ず環境予約を確保すること。対象はチーム自身の環境のみ。

## 現在のcheckoutをdeployして一度だけ測る
環境予約を別途保持した人が、`scripts/measure-current.sh OPERATOR LABEL` を使う。この一回限りのfail-closed chainは、開始時HEADを固定し、全unmerged indexと `go/sql/public` のdirty/untrackedを拒否する。既存 `scripts/deploy.sh` の成功と最終stdout SHAの一致を要求し、deploy後に同じsource状態を再確認してから、operatorと要求SHAを明示して既存 `portal-bench run` を一度だけ呼ぶ。失敗時はベンチを開始しない。stdoutは既存のportal JSONのみ、deploy出力と証拠（host binary hashを含む）はstderrへ出す。

SHA照合はdeploy scriptが返す要求checkout SHAの確認であり、remoteの全ファイルをattestしない。dirty判定は `go/sql/public` のみで、nginx設定などは対象外。このhelperは予約、DDL/migration、promotion、復旧を行わず、Main独自の別custom callerへ自動適用されない。別callerはそこで同じfail-fast条件を明示的に確認すること。
