# 参加者のセットアップ

## 各PC

Git、Python 3.9以上、利用するClaude CodeまたはCodex、AWS CLI v2を用意します。AWSは各自のISUCON用アカウント権限で認証し、認証情報は共有しません。リポジトリと共通ホストへのチームメンバーのアクセス権追加は、対象のGitHub名・AWS principalを確認して行います。

```bash
git clone https://github.com/shm11C3/isucon-competition.git
cd isucon-competition
aws sts get-caller-identity
python3 scripts/control.py status
```

`config/control-host.json`のaccountと認証先が異なる場合は操作を止めます。`control.py`はこの一致を検査します。別profileを使う場合は `AWS_PROFILE` を各自で設定します。

## Claude Code

`CLAUDE.md`が `@AGENTS.md` を取り込みます。kaizenを使う場合は `config/plugin.json` のrepositoryを別ディレクトリへcloneし、固定revisionをcheckoutして起動します。

```bash
git clone https://github.com/shm11C3/kaizen-plugin.git ../kaizen-plugin-competition
git -C ../kaizen-plugin-competition checkout --detach 187200363e980f208b64040537e6357384604da3
claude --plugin-dir ../kaizen-plugin-competition
```

共通環境のkaizenを操作するのは統合担当だけです。各workerがローカルのkaizenで同じ競技環境の実験を独立に開始してはいけません。

## Codex

リポジトリのルートを作業場所にして開始します。Codexは `AGENTS.md` を読みます。kaizenの `skills/*/SKILL.md` とスクリプトを参照し、同じ規約に従います。現在のkaizenにはCodex用hook adapterがないため、Claudeのhookと同等の強制があるとはみなしません。

```bash
codex
```

## 最初の指示例

```text
AGENTS.md、docs/OPERATIONS.md、docs/RUNBOOK.mdを読み、セッションIDを
a-codex-01として参加してください。最新の共有状態と観測を確認し、
根拠のある課題の発見・起票・取得・実装・候補提出・次の作業まで自走してください。
統合担当の権限は環境占有を取得した場合に限ります。
人間判断条件に当てはまるものは、安全な検証と比較を終えてから相談してください。
競技対象が未設定の間は、競技環境への操作を開始しないでください。
```

## 共通ホスト

通常の台帳操作は `scripts/control.py` がSSM Run Commandを使うため、Session Manager pluginは不要です。対話シェルにはAWS CLIのSession Manager pluginが必要です。AWSコンソールのSession Managerからも接続できます。

```bash
python3 scripts/control.py shell
# 接続後
sudo -iu isucon
cd /srv/isucon/repo
```

中央のディレクトリは、Git cloneが `/srv/isucon/repo`、固定プラグインが `/srv/isucon/kaizen-plugin`、台帳が `/srv/isucon/state`、大きなログが `/srv/isucon/artifacts` です。GitHubのwrite deploy keyはこのリポジトリだけ、プラグイン用keyは読み取りだけです。秘密鍵は共通ホスト内に保持します。

## 参照

- [CodexのAGENTS.md](https://developers.openai.com/codex/guides/agents-md/)
- [Claude Codeのメモリとimport](https://code.claude.com/docs/en/memory)
- [Session Managerの前提条件](https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-prerequisites.html)
