# T-0075 TCP 設定（asia1 / america1 / asia2、設定のみ）

適用:
    sudo cp etc-common/sysctl.d/90-isureal-tcp.conf /etc/sysctl.d/ && sudo sysctl -p /etc/sysctl.d/90-isureal-tcp.conf
    sudo cp etc-common/systemd/system/isureal-tcp-initcwnd.service /etc/systemd/system/ && sudo systemctl daemon-reload && sudo systemctl enable --now isureal-tcp-initcwnd
確認: `sysctl net.ipv4.tcp_slow_start_after_idle` が 0、`ip route show default` に `initcwnd 64 initrwnd 64`。
戻し: `sudo systemctl disable --now isureal-tcp-initcwnd`、`sudo sysctl -w net.ipv4.tcp_slow_start_after_idle=1`、`ip route change <元の行>`。
狙い: 地域間 100ms リンク上の 500KB 画像転送（T68 系の asia1→asia2 は LAN なので主に europe1/america1→primary の投稿と WAL）。
