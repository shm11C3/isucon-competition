#!/bin/bash
# Build and restart the current checkout on all app servers. Run on an idle environment.
set -euo pipefail
cd -- "$(dirname -- "$0")/.."
export CFG=${ISU_SSH_CONFIG:-../ssh_config_2026}
checkout_sha=$(git rev-parse HEAD)
printf 'deploy checkout_sha=%s\n' "$checkout_sha" >&2
if test -n "$(git status --porcelain -- go sql public)"; then
	printf 'warning: local source changes are included; checkout SHA alone does not identify this deployment\n' >&2
fi
america_unit_state() {
	local host=$1 output load_state unit_state
	if ! output=$(ssh -F "$CFG" -o BatchMode=yes "$host" 'systemctl show isureal-go-america.service -p LoadState -p UnitFileState'); then
		printf 'deploy preflight failed: cannot inspect America unit on %s\n' "$host" >&2
		return 1
	fi
	load_state=$(printf '%s\n' "$output" | awk -F= '$1 == "LoadState" { count++; value = substr($0, index($0, "=") + 1) } END { if (count == 1) print value; else exit 1 }') || {
		printf 'deploy preflight failed: invalid LoadState from %s\n' "$host" >&2
		return 1
	}
	unit_state=$(printf '%s\n' "$output" | awk -F= '$1 == "UnitFileState" { count++; value = substr($0, index($0, "=") + 1) } END { if (count == 1) print value; else exit 1 }') || {
		printf 'deploy preflight failed: invalid UnitFileState from %s\n' "$host" >&2
		return 1
	}
	case "$load_state:$unit_state" in
		loaded:enabled|loaded:enabled-runtime) printf 'enabled\n' ;;
		not-found:*) printf 'disabled\n' ;;
		loaded:disabled) printf 'disabled\n' ;;
		*) printf 'deploy preflight failed: ambiguous America unit state on %s (LoadState=%s UnitFileState=%s)\n' "$host" "$load_state" "$unit_state" >&2; return 1 ;;
	esac
}
asia1_america_state=$(america_unit_state asia1) || exit 1
asia2_america_state=$(america_unit_state asia2) || exit 1
if [[ $asia1_america_state == enabled && $asia2_america_state == disabled ]]; then
	america_owner=asia1
elif [[ $asia1_america_state == disabled && $asia2_america_state == enabled ]]; then
	america_owner=asia2
else
	printf 'deploy preflight failed: expected exactly one enabled America unit (asia1=%s asia2=%s)\n' "$asia1_america_state" "$asia2_america_state" >&2
	exit 1
fi
printf 'deploy America owner=%s (asia1=%s asia2=%s)\n' "$america_owner" "$asia1_america_state" "$asia2_america_state" >&2
deploy() {
	local h=$1
	rsync -a -e "ssh -F $CFG -o BatchMode=yes" --exclude '._*' --exclude 'go/isureal' go sql public "$h:webapp/"
	local america_unit=':'
	if [[ $h == asia1 && $america_owner == asia1 ]]; then
		america_unit='set -- "$@" isureal-go-america'
	fi
	local remote_script='set -eu
		cd ~/webapp/go
		PATH=$HOME/local/go/bin:$PATH go build -o isureal.new cmd/isureal/main.go
		mv isureal.new isureal
		grep -q ISUREAL_LOG_LEVEL ~/env.sh || echo ISUREAL_LOG_LEVEL=info >> ~/env.sh
		set -- isureal-go
		if test "$(hostname)" = ip-192-168-1-10; then
			set -- "$@" isureal-go-europe
		fi
		__AMERICA_UNIT__
		sudo systemctl restart "$@"
		sleep 1
		for unit in "$@"; do
			systemctl is-active "$unit"
		done
		printf "host=%s binary_sha256=" "$(hostname)"
		sha256sum isureal'
	remote_script=${remote_script/__AMERICA_UNIT__/$america_unit}
	ssh -F "$CFG" -o BatchMode=yes "$h" "$remote_script"
}
hosts=(asia1 europe1 america1)
deploy_asia2() {
	# America also executes ../sql/init.sh here; keep reset SQL with its binary.
	rsync -a -e "ssh -F $CFG -o BatchMode=yes" --exclude '._*' sql asia2:webapp/ || {
		printf 'deploy failed: asia2 SQL transfer\n' >&2
		return 1
	}
	if [[ $america_owner == asia1 ]]; then
		printf 'deploy succeeded: asia2 SQL; America Go remains on asia1\n' >&2
		return 0
	fi
	# Stage bytes first. A failed source SSH must not replace the running binary.
	ssh -F "$CFG" -o BatchMode=yes asia1 'cat ~/webapp/go/isureal' |
		ssh -F "$CFG" -o BatchMode=yes asia2 'set -eu; mkdir -p ~/webapp/go; cat > ~/webapp/go/isureal.new' || {
			printf 'deploy failed: asia2 binary transfer\n' >&2
			return 1
		}
	ssh -F "$CFG" -o BatchMode=yes asia2 'set -eu
		cd ~/webapp/go
		chmod +x isureal.new
		mv isureal.new isureal
		sudo systemctl restart isureal-go-america
		sleep 1
		systemctl is-active isureal-go-america
		printf "host=%s binary_sha256=" "$(hostname)"
		sha256sum isureal' || {
			printf 'deploy failed: asia2 activation\n' >&2
			return 1
		}
	printf 'deploy succeeded: asia2\n' >&2
}
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
deploy_asia2
printf '%s\n' "$checkout_sha"
