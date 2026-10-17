# ISUCON 当日用リポジトリ

人間3人がClaude・Codexを使い、調査・実装を並列に進めるための作業場所です。GitHubはPrivate Gitリモートとして使います。PR・レビュー待ちは競技の手順に含めません。

競技アプリは `app/`、配備する設定は `etc/` に当日取り込みます。現在は両方とも空です。競技用サーバー・ベンチマークは未設定で、対象環境を変更するコマンドは無効です。

## 最初に読む

- [運用ガイド](docs/OPERATIONS.md)：役割、自走ループ、Git、計測、採否
- [当日の手順](docs/RUNBOOK.md)：開始・通常運用・終了・障害復旧
- [参加者のセットアップ](docs/ONBOARDING.md)：Claude・Codex、AWS、共通ホスト
- [インフラ](infra/README.md)：共通EC2の構築・接続・停止・再構築
- [AGENTS.md](AGENTS.md)：両エージェント共通の規約。Claudeは `CLAUDE.md` から取り込みます

## 構成

```text
app/                  当日取り込む競技アプリ（空）
etc/                  当日取り込む配備対象設定（空）
config/               接続先の非機密情報、設定例、プラグインの固定revision
docs/                 運用ガイド、当日手順、セットアップ
templates/            候補提出・比較検討・ベンチ記録・引き継ぎ
records/              統合担当がGitに保存する採否・計測の記録
scripts/team.py       共通ホスト上の原子的なタスク取得と環境占有
scripts/control.py    各PCからSSM経由でteam.pyを実行
scripts/target.py     占有中の統合担当がkaizenのスクリプトを実行
infra/                EC2構築スクリプトと構築記録
```

## 運用の約束

1. AIは課題発見・起票・取得・実装・候補提出・次の作業まで自走します。
2. 通常の改善は、正しさ・比較条件・性能改善・復旧可能性を確認して自動採用します。
3. 採否が悩ましい、大きくリスクがある、重要なトレードオフがある、構成や方針に複数の有力案がある変更は人間が判断します。
4. タスク状態と共有環境の占有は共通ホストで管理します。Discordは発見・通知・相談の窓口です。
5. `main`更新・デプロイ・観測設定変更・ベンチ・復旧は、その時点の単一の統合担当だけが行います。

## ローカルの確認

```bash
python3 -m unittest discover -s tests -v
python3 scripts/control.py --help
python3 scripts/team.py --help
```

このリポジトリは自動起動するAIランタイムやDiscord Botを含みません。各自が起動したセッションで継続ループを回します。現在のkaizenプラグインは単一実験を管理するため、共有環境での実験は統合担当の1つに集約します。
