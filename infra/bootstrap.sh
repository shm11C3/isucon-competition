#!/usr/bin/env bash
set -euo pipefail
dnf install -y git python3 jq tmux rsync util-linux openssh-clients
id isucon >/dev/null 2>&1 || useradd --create-home --shell /bin/bash isucon
install -d -o isucon -g isucon -m 0750 /srv/isucon
install -d -o isucon -g isucon -m 0700 /srv/isucon/state /srv/isucon/artifacts
install -d -o isucon -g isucon -m 0700 /home/isucon/.ssh
timedatectl set-timezone Asia/Tokyo
systemctl enable --now amazon-ssm-agent
touch /var/lib/isucon-control-bootstrap-complete
