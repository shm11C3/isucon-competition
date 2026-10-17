# 共通ホスト

統合用Git clone、kaizen、台帳、計測記録を1台に集約します。各PCのAIが判断し、このホストのコマンドを操作します。AI自体の常駐サービスは作りません。

## 構成と意図

- Amazon Linux 2023 x86_64、t3.small（2 vCPU / 2 GiB）、暗号化gp3 30 GiB。
- 競技アプリの実行環境とは分離。既存の練習用インスタンスとSecurity Groupを変更しない。
- 専用Security Groupはインバウンドルールなし。SSMのために既存のpublic subnetから外向き通信を行う。public IPv4は外向き接続用で、SSHやWebの公開口はない。NAT Gateway・Elastic IPは作らない。
- IMDSv2必須。EC2のroleはAmazonSSMManagedInstanceCoreだけで、EC2作成・IAM変更などの管理権限を持たせない。
- CPU creditはstandard。継続的なビルド能力が必要になった場合は、使用量を見て人間がサイズ変更を判断する。
- SQLiteと実験状態はEBS上に置く。root volumeはインスタンス終了時に削除されるので、終了前に必ずバックアップする。
- provisioningはAWS CLIのAPIで行い、既存の練習用インフラとライフサイクルを分離する。CloudFormationスタックは作らない。

## 構築・再実行

```bash
python3 infra/provision.py          # accountと構成を確認。作成しない
python3 infra/provision.py --create # このprojectの共通ホストだけ作成
```

設定は `config/infra.json`、作成結果は `config/control-host.json`。`--create`の再実行は同じタグの既存インスタンスを使い、勝手に追加・再起動しない。role・profile・Security Groupの同名リソースに所有確認ができない場合は止まる。既存ホストのAMIや構成を更新する操作ではない。

bootstrapはgit・Python・jq・tmux・rsyncを導入し、isuconユーザーと専用ディレクトリを作る。完了印は `/var/lib/isucon-control-bootstrap-complete`。

Privateリポジトリを作成・pushし、SSMがOnlineになった後に、認証済みAWS CLIとGitHub CLIを持つPCで実行する。

```bash
python3 infra/initialize-host.py
```

この操作は共通ホスト内で2つのSSH鍵を生成し、当日リポジトリにはwrite deploy key、プラグインにはread-only deploy keyを登録する。秘密鍵はホスト外へ出さない。GitHubのホスト鍵は公式APIの公開鍵を使う。clone、プラグインのrevision固定、ホスト上のテスト、空の台帳の初期化まで行う。既存cloneではfetchとfast-forwardを行い、作業内容をresetしない。環境使用中には初期化を再実行しない。

## 接続

```bash
python3 scripts/control.py status
python3 scripts/control.py shell
```

通常の台帳操作はSSM Run Command。shellはSession Manager plugin、またはAWSコンソールから接続する。チームメンバーには、それぞれのAWS principalへこのホストのSSM操作権限を付与する。共通ホストのEC2 roleは、人間がホストに接続するための権限とは別。

## 状態のバックアップ

統合の区切りで実験要約を `records/` にcommitし、mainをpushする。生ログ・未完了タスク・`.kaizen`はGitだけでは保護されない。SQLiteは稼働中のファイルを単純コピーせず、backup APIを使う。

```bash
# 共通ホストで、操作が止まった安定した区切りに実行
sudo -iu isucon python3 - <<'PY'
import sqlite3
from pathlib import Path
dest = Path('/srv/isucon/artifacts/team-backup.sqlite3')
with sqlite3.connect('/srv/isucon/state/team.sqlite3') as src, sqlite3.connect(dest) as dst:
    src.backup(dst)
PY
```

インスタンス削除前には、外部へのartifact退避かEBS snapshotを取得して復元できることを確認する。snapshotとその保持には別途料金がかかる。新しいホストへ復旧するときはGit、台帳、`.kaizen`、artifactを戻し、GitHub deploy keyを再発行し、各競技ホストの実状態を確認する。占有を消すことを復旧手順にしてはいけない。

ホストを廃棄するときは、両リポジトリの該当deploy keyも失効させる。key IDは構築記録に残す。

## 停止・再開

`config/control-host.json`のIDを指定する。共通ホストだけを操作し、既存の練習用・競技用サーバーには適用しない。

```bash
aws ec2 stop-instances --region ap-northeast-1 --instance-ids CONTROL_INSTANCE_ID
aws ec2 start-instances --region ap-northeast-1 --instance-ids CONTROL_INSTANCE_ID
```

停止前に処理・ベンチの終了と記録を確認する。停止してもEBS料金は続く。再開後はSSM Onlineと保存状態を確認する。public IPv4が変わっても接続はinstance IDで行う。

## 料金と監査

2026-10-17にAWS Price List APIで確認した東京リージョンのLinux t3.small On-Demandは **0.0272 USD/時間**。EC2本体だけなら24時間で0.6528 USD、730時間で19.856 USD。EBS、public IPv4、転送、snapshot等は別。予算アラートの金額・通知先はチームで決めて設定する。

構築時点でこのアカウントにCloudTrail trailは確認できなかった。SSM Command履歴とCloudTrail Event historyによるAPI確認は使えるが、長期trailやSession Managerのセッション内容ログは未設定。機密情報をコマンド本文・出力へ含めない。

## 参照

- [AL2023のAMIとSSM parameter](https://docs.aws.amazon.com/linux/al2023/ug/ec2.html)
- [Session Managerの前提条件](https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-prerequisites.html)
- [EC2料金](https://aws.amazon.com/ec2/pricing/on-demand/)
