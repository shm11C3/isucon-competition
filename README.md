# ISUCON 当日用リポジトリ

競技の初期コードを当日取り込むための空の作業場所です。
各メンバーがこのリポジトリをcloneし、kaizenプラグインをインストールして参加します。

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
