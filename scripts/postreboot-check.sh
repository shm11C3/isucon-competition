#!/bin/bash
# Read-only health check for the final topology. Run from a PC: scripts/postreboot-check.sh [phase]
# Prints one block per host; compare "before" and "after" blocks by eye.
set -u
cd -- "$(dirname -- "$0")/.."
CFG=${ISU_SSH_CONFIG:-../ssh_config_2026}
phase=${1:-check}
r() { ssh -F "$CFG" -o BatchMode=yes -o ConnectTimeout=8 "$@"; }
echo "== $phase $(date +%H:%M:%S)"
for h in asia1 asia2 europe1 america1; do
	echo "-- $h"
	r "$h" '
		echo "uptime: $(cut -d. -f1 /proc/uptime)s boot_id=$(cut -c1-8 /proc/sys/kernel/random/boot_id)"
		systemctl list-units --type=service --state=active --no-legend "isureal*" "postgresql@*" "nginx*" | awk "{printf \"%s \", \$1}"; echo
		sudo -n nginx -t >/dev/null 2>&1 && echo "nginx: config ok" || echo "nginx: n/a or broken"
		if systemctl is-active -q postgresql@16-main; then
			echo "in_recovery(standby=t): $(sudo -n -u postgres psql -Atc "select pg_is_in_recovery()" 2>/dev/null)"
			sudo -n -u postgres psql -Atc "select concat_ws(chr(58), application_name, state, sync_state) from pg_stat_replication" 2>/dev/null | tr "\n" " "; echo
		fi
		test -d /home/isucon/image-store && echo "image-store files: $(find /home/isucon/image-store -type f | wc -l)"
		sudo -n /usr/local/bin/envcheck 2>&1 | grep -ao "全ての追試環境チェックをパスしました" || echo "envcheck: NOT PASSED"
		echo "mem avail: $(free -m | awk "NR==2{print \$7}")MB oom: $(sudo -n dmesg | grep -c oom-kill:)"
	' 2>&1 | sed 's/^/   /'
done
echo "-- data (primary, since initial boundary)"
r asia2 'sudo -n -u postgres psql -d isureal -At -c "
  select (select count(*) from posts where created_at > (select created_at from initial_data_boundary)) posts,
         (select count(*) from image_objects where created_at > (select created_at from initial_data_boundary)) images,
         (select count(*) from comments where created_at > (select created_at from initial_data_boundary)) comments,
         (select count(*) from realmojis where created_at > (select created_at from initial_data_boundary)) realmojis,
         (select md5(string_agg(id, chr(44) order by id)) from posts where created_at > (select created_at from initial_data_boundary)) post_ids"' | sed 's/^/   /'
echo "-- router"
curl -sk -m 8 -o /dev/null -w '   https / -> %{http_code}\n' https://35.74.223.182/ -H 'Host: app.team60055-aws.127.0.0.1.nip.io'
for cc in JP DE US; do curl -sk -m 8 -o /dev/null -w "   $cc /api/posting-window/current -> %{http_code}\n" -H "X-Country-Code: $cc" https://35.74.223.182/api/posting-window/current -H 'Host: app.team60055-aws.127.0.0.1.nip.io'; done
curl -sk -m 8 -o /dev/null -w '   JP POST /api/posts (no auth) -> %{http_code}\n' -X POST -H 'X-Country-Code: JP' https://35.74.223.182/api/posts -H 'Host: app.team60055-aws.127.0.0.1.nip.io'
