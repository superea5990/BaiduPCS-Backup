#!/usr/bin/env bash
# BaiduPCS-Backup 免安装包同步脚本
# 用法: 在 Git Bash 中执行  ./sync_portable.sh
# 作用: 重新编译 GUI-Lite -> 同步到 BaiduPCS-Backup-Portable -> 重新打包 zip
# 可选: 同时刷新已装目录(默认跳过, 避免覆盖正在运行的程序):
#        INSTALL_DIR="D:/Program Files (x86)/BaiduPCS-Backup" ./sync_portable.sh
set -e

ROOT="$(cd "$(dirname "$0")" && pwd)"
LITE="$ROOT/BaiduPCS-Backup-GUI-Lite"
CORE="$ROOT/BaiduPCS-Go"
PORT="$ROOT/BaiduPCS-Backup-Portable"

echo "[1/4] 编译 BaiduPCS-Backup-GUI (GUI-Lite, 当前主版本)..."
cd "$LITE"
go build -ldflags "-H windowsgui -s -w" -o BaiduPCS-Backup-GUI.exe .

echo "[2/4] 同步 GUI 与核心程序到免安装包..."
cp -f "$LITE/BaiduPCS-Backup-GUI.exe" "$PORT/BaiduPCS-Backup-GUI.exe"
cp -f "$CORE/BaiduPCS-Backup.exe"      "$PORT/BaiduPCS-Go/BaiduPCS-Backup.exe"
cp -f "$LITE/netdisk_logo.ico"         "$PORT/netdisk_logo.ico"

echo "[3/4] 清理免安装包内的本机状态 (账号/任务/日志)..."
rm -rf "$PORT/gui_config.json" "$PORT/mode_config.json" "$PORT/log" "$PORT/BaiduPCS-Go/log"
rm -f  "$PORT"/backup-task.*.json "$PORT"/BaiduPCS-Go/backup-task.*.json

echo "[4/4] 打包 BaiduPCS-Backup-Portable.zip..."
rm -f "$ROOT/BaiduPCS-Backup-Portable.zip"
"/c/Windows/System32/tar.exe" -a -c -f "$ROOT/BaiduPCS-Backup-Portable.zip" -C "$ROOT" BaiduPCS-Backup-Portable

echo "完成: $PORT"
ls -la "$PORT" "$ROOT/BaiduPCS-Backup-Portable.zip"

# [可选] 刷新已装目录: 仅在显式传入 INSTALL_DIR 时执行
if [ -n "$INSTALL_DIR" ] && [ -d "$INSTALL_DIR" ]; then
	echo "[5/5] 刷新已装目录: $INSTALL_DIR"
	cp -f "$LITE/BaiduPCS-Backup-GUI.exe" "$INSTALL_DIR/BaiduPCS-Backup-GUI.exe"
	cp -f "$CORE/BaiduPCS-Backup.exe"      "$INSTALL_DIR/BaiduPCS-Go/BaiduPCS-Backup.exe"
	cp -f "$PORT/README.txt"               "$INSTALL_DIR/README.txt"
	echo "已装目录刷新完成 (gui_config.json / mode_config.json / log 保留不动)"
else
	echo "[5/5] 跳过已装目录刷新 (未设置 INSTALL_DIR)"
fi
