#!/bin/bash
# Build and restart the current checkout on all app servers. Run on an idle environment.
set -eu
cd -- "$(dirname -- "$0")/.."
export CFG=${ISU_SSH_CONFIG:-../ssh_config_2026}
checkout_sha=$(git rev-parse HEAD)
printf 'deploy checkout_sha=%s\n' "$checkout_sha" >&2
if test -n "$(git status --porcelain -- go sql public)"; then
	printf 'warning: local source changes are included; checkout SHA alone does not identify this deployment\n' >&2
fi
deploy() {
	local h=$1
	rsync -a -e "ssh -F $CFG -o BatchMode=yes" --exclude '._*' --exclude 'go/isureal' go sql public "$h:webapp/"
	ssh -F "$CFG" -o BatchMode=yes "$h" 'set -eu
		cd ~/webapp/go
		PATH=$HOME/local/go/bin:$PATH go build -o isureal.new cmd/isureal/main.go
		mv isureal.new isureal
		grep -q ISUREAL_LOG_LEVEL ~/env.sh || echo ISUREAL_LOG_LEVEL=info >> ~/env.sh
		sudo systemctl restart "isureal-go*"
		sleep 1
		systemctl is-active isureal-go
		printf "host=%s binary_sha256=" "$(hostname)"
		sha256sum isureal'
}
hosts=(asia1 europe1 america1)
pids=()
for h in "${hosts[@]}"; do
	deploy "$h" &
	pids+=("$!")
done
failed=0
for i in "${!pids[@]}"; do
	# Keep wait in the parent: putting deploy itself in an if disables errexit
	# inside the function and can turn a failed rsync into a stale restart.
	if wait "${pids[$i]}"; then
		printf 'deploy succeeded: %s\n' "${hosts[$i]}" >&2
	else
		printf 'deploy failed: %s\n' "${hosts[$i]}" >&2
		failed=1
	fi
done
if test "$failed" -ne 0; then
	exit 1
fi
printf '%s\n' "$checkout_sha"
