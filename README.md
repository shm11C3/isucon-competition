# ISUCON 当日用リポジトリ

最終有効スコアを改善するための最小の入口です。競技の初期コードを当日取り込みます。
各メンバーがこのリポジトリをcloneし、kaizenプラグインをインストールして参加します。

## 最初の動き

人間の指定したpractice/competition、終了時刻、対象環境、連絡チャンネルを確認し、
teamスキルの短いSTARTから参加します。join後は最新の議論とqueueを確認し、
観測または候補準備を始めます。詳しい手順は実際に必要な操作の前に開きます。
練習・本番とも素早いフィードバックを優先し、確認は変更した契約へ絞ります。
環境排他・アプリの正しさ・採否と復旧の条件はプラグインに従います。

## forkで模擬練習する

次の検証・模擬練習は、このリポジトリから専用の非公開forkを作って始めます。
そのforkへ対象問題の初期コードを取り込み、練習専用のhost-config・台帳・資源を設定します。
本番用の接続先を引き継いだまま操作しないでください。問題固有のコード、IP、認証情報、
スコアや生ログは本番の入口リポジトリへ戻しません。共通の改善だけをkaizenへ反映します。
人間が求めた中間レトロは行えますが、本番の必須儀式にはしません。

## Claude Code

```bash
claude plugin marketplace add shm11C3/kaizen-plugin
claude plugin install kaizen@kaizen-plugin
```

このディレクトリで新しいセッションを開始し、`/kaizen:team` を実行します。

## Codex

```bash
codex plugin marketplace add shm11C3/kaizen-plugin
codex plugin add kaizen@kaizen-plugin
```

このディレクトリで新しいセッションを開始し、「kaizenプラグインのteamスキルで参加」と指示します。

運用ガイド・共通ホスト管理・コマンド・テンプレートは
[kaizen-plugin](https://github.com/shm11C3/kaizen-plugin) にあります。
[参加手順](https://github.com/shm11C3/kaizen-plugin/blob/main/team/docs/ONBOARDING.md)を参照してください。
Privateリポジトリへのアクセス権と、各自のAWS認証を事前に準備します。
