#!/bin/bash
# Build and restart the current checkout on all app servers. Run on an idle environment.
set -eu
cd -- "$(dirname -- "$0")/.."
export CFG=${ISU_SSH_CONFIG:-../ssh_config_2026}
deploy() {
	local h=$1
	rsync -a -e "ssh -F $CFG -o BatchMode=yes" --exclude '._*' --exclude 'go/isureal' go sql public "$h:webapp/"
	ssh -F "$CFG" -o BatchMode=yes "$h" 'cd ~/webapp/go && PATH=$HOME/local/go/bin:$PATH go build -o isureal.new cmd/isureal/main.go && mv isureal.new isureal
		grep -q ISUREAL_LOG_LEVEL ~/env.sh || echo ISUREAL_LOG_LEVEL=info >> ~/env.sh
		sudo systemctl restart isureal-go && sleep 1 && systemctl is-active isureal-go'
}
for h in asia1 europe1 america1; do deploy "$h" & done
wait
git rev-parse --short HEAD
