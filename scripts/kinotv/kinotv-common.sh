#!/usr/bin/env bash
# KinoTV 运维脚本共用的可移植封装。
#
# 这些脚本两头都要跑：本地 macOS 做演练，服务器 CentOS/Ubuntu 做正式任务。
# 两边的 stat / 校验和工具参数不一样，散在各自脚本里迟早会出现"本地过了、线上报错"
# 或反过来，所以统一收敛到这里。

# 取文件字节数。GNU stat 与 BSD stat 的参数不兼容，这里只认输出。
kinotv_file_size() {
    local path="$1"
    if stat -c %s "$path" >/dev/null 2>&1; then
        stat -c %s "$path"
    else
        stat -f %z "$path"
    fi
}

# 取文件 mtime（epoch 秒）。
kinotv_file_mtime() {
    local path="$1"
    if stat -c %Y "$path" >/dev/null 2>&1; then
        stat -c %Y "$path"
    else
        stat -f %m "$path"
    fi
}

# 取文件权限（八进制三位），用于断言备份里的 .settings-key 没被放宽。
kinotv_file_mode() {
    local path="$1"
    if stat -c %a "$path" >/dev/null 2>&1; then
        stat -c %a "$path"
    else
        stat -f %Lp "$path"
    fi
}

# 取 inode 号。媒体备份用硬链接去重，"这份备份和上一份是不是同一个文件"靠它判断，
# 比拿 size+mtime 猜可靠——素材是只增不改的，inode 相同就一定没被改过。
kinotv_file_inode() {
    local path="$1"
    if stat -c %i "$path" >/dev/null 2>&1; then
        stat -c %i "$path"
    else
        stat -f %i "$path"
    fi
}

# 计算 sha256。优先 coreutils 的 sha256sum，macOS 只有 shasum。
kinotv_sha256() {
    local path="$1"
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$path" | awk '{print $1}'
    else
        shasum -a 256 "$path" | awk '{print $1}'
    fi
}

# 人类可读的字节数，日志里用。
kinotv_human_size() {
    local bytes="$1"
    if (( bytes >= 1048576 )); then
        printf '%dMB' $(( bytes / 1048576 ))
    elif (( bytes >= 1024 )); then
        printf '%dKB' $(( bytes / 1024 ))
    else
        printf '%dB' "$bytes"
    fi
}

# 当前时间的 ISO-8601 字符串。GNU date 支持 -Is，BSD date 不支持，
# 而这两个脚本都要在本地 macOS 上跑演练，所以自己拼。
kinotv_iso_now() {
    date +%Y-%m-%dT%H:%M:%S%z | sed -E 's/([+-])([0-9]{2})([0-9]{2})$/\1\2:\3/'
}

kinotv_log() {
    printf '[%s] %s\n' "$(date +%Y%m%d_%H%M%S)" "$*"
}
