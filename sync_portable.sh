#!/usr/bin/env bash
# BaiduPCS-Backup 免安装包同步脚本
# 用法: 在 Git Bash 中执行  ./sync_portable.sh
# 作用: 重新编译 GUI-Lite -> 自检产物 -> 同步到 BaiduPCS-Backup-Portable -> 重新打包 zip
# 可选: 同时刷新已装目录(默认跳过, 避免覆盖正在运行的程序):
#        INSTALL_DIR="D:/Program Files (x86)/BaiduPCS-Backup" ./sync_portable.sh
set -e

ROOT="$(cd "$(dirname "$0")" && pwd)"
LITE="$ROOT/BaiduPCS-Backup-GUI-Lite"
CORE="$ROOT/BaiduPCS-Go"
PORT="$ROOT/BaiduPCS-Backup-Portable"
OUT="$LITE/BaiduPCS-Backup-GUI.exe"

# 保证能找到 go：Git Bash 里常常没把 Go 加进 PATH
export PATH="$PATH:/c/Program Files/Go/bin"

# 体积下限：正常带 manifest 的 GUI 约 7.5 MB。明显小于这个值，基本就是
# 没链接上 rsrc_windows_amd64.syso（丢 manifest -> GUI 静默崩溃）。
MIN_SIZE=7000000

# ------------------------------------------------------------
# check_gui_exe <exe 路径>
#   编译后自检：确认产物是「GUI 子系统 + 已嵌资源」的正品。
#
#   【为什么非要读 PE 头 —— 别删】
#     go build 漏了 -ldflags "-H windowsgui" 时，产物会被链接成
#     CONSOLE 子系统（PE Optional Header 的 Subsystem 字段 = 3）。
#     Windows 会顺手给这种程序分配一个控制台黑窗，而那个黑窗和 GUI
#     主窗口属于同一个进程 —— 用户关掉黑窗，整个进程被终止，GUI 也就
#     跟着退出了。这正是 v1.1.0 反馈的「弹黑窗、关黑窗连主程序一起关」。
#
#     这种手误肉眼完全看不出来：exe 双击照样能启动、功能也是对的，
#     只有 PE 头里的一个 WORD 不对。所以必须在脚本里自动卡住，
#     让问题当场暴露，而不是等到用户报障再回头查几个小时。
#
#   只依赖 Git Bash 自带的 od / wc，不引入 Python 等外部依赖。
# ------------------------------------------------------------
check_gui_exe() {
	local exe="$1"
	local min_override="$2"
	local label="${3:-GUI}"
	local size magic lfanew pe_sig opt_magic subsystem min_size
	local ps_desc om_desc
	local err=0

	# 体积下限的取值优先级：$2 > 环境变量 MIN_SIZE > 内置默认 7000000。
	# 为什么不干脆用全局变量覆盖：因为那样调用方必须记得改回来，一旦漏改，
	# 后面再检查 GUI 就会沿用核心的门槛 —— 错误的阈值会静默放行，
	# 这正是这道闸要防的事。显式传参不存在这种残留状态。
	min_size="${min_override:-${MIN_SIZE:-7000000}}"

	if [ ! -f "$exe" ]; then
		echo "✗ ${label} 产物不存在，无法自检：$exe"
		return 1
	fi

	# ------------------------------------------------------------
	# 第一关：PE 结构合法性 —— 必须逐级校验，不能只认一个 MZ。
	#   "MZ" 谁都能往文件头写两个字节；如果接着就信任 e_lfanew 去推偏移量，
	#   那么只要伪造 e_lfanew、再在推算出的偏移处填一个 2、然后把文件撑到
	#   体积下限以上，一个全零的假文件就能伪装成"合法的 GUI 子系统 exe"
	#   蒙混过关。所以下面每一步都必须各自具备失败能力（fail-closed）。
	# ------------------------------------------------------------

	# DOS 头魔数
	magic="$(od -An -c -N 2 "$exe" 2>/dev/null | tr -d '[:space:]')"
	# 文件长度：后面所有偏移量都要拿它做边界检查
	size="$(wc -c < "$exe" | tr -d '[:space:]')"
	# e_lfanew：0x3C(=60) 处的 DWORD，指向 PE 签名
	lfanew="$(od -An -tu4 -j 60 -N 4 "$exe" 2>/dev/null | tr -d '[:space:]')"

	case "${size:-}" in
		''|*[!0-9]*)
			echo "✗【致命】无法解析 ${label} 产物体积：'${size:-<空>}'"
			return 1
			;;
	esac

	if [ "$magic" != "MZ" ]; then
		echo "✗【致命】不是有效的 PE 文件（DOS 头 MZ 魔数缺失）：$exe"
		err=1
	fi

	case "${lfanew:-}" in
		''|*[!0-9]*)
			echo "✗【致命】无法解析 e_lfanew（偏移 0x3C 的 DWORD）：'${lfanew:-<空>}'"
			lfanew=0
			err=1
			;;
	esac

	# 边界检查：要读到 Optional Header 里 offset 68 的 Subsystem(2B)，
	# 文件至少要有 e_lfanew + 4(PE 签名) + 20(COFF 头) + 68 + 2 = e_lfanew + 94 字节。
	if [ "$err" -eq 0 ] && [ "$size" -lt $((lfanew + 94)) ]; then
		echo "✗【致命】PE 头越界：文件仅 $size 字节，读到 Subsystem 至少需要 $((lfanew + 94)) 字节"
		err=1
	fi

	pe_sig=""
	opt_magic=""
	subsystem=""
	if [ "$err" -eq 0 ]; then
		# PE 签名必须是 50 45 00 00（"PE" 后跟两个 0 字节）。
		# 少了它说明 e_lfanew 指的压根不是 PE 头，后面按它推的偏移全是垃圾数据。
		pe_sig="$(od -An -tx1 -j "$lfanew" -N 4 "$exe" 2>/dev/null | tr -d '[:space:]')"
		# Optional Header Magic：267=0x10B(PE32) / 523=0x20B(PE32+)，其他值都不是 PE 映像
		opt_magic="$(od -An -tu2 -j $((lfanew + 24)) -N 2 "$exe" 2>/dev/null | tr -d '[:space:]')"
		# Subsystem：Optional Header offset 68 处的 WORD。
		#   PE32 与 PE32+ 在这个偏移上的位置相同（ImageBase 宽度差在它之前已被吸收）。
		#   2 = WINDOWS_GUI（期望值，没有黑窗）  3 = WINDOWS_CUI（有黑窗，不合格）
		subsystem="$(od -An -tu2 -j $((lfanew + 24 + 68)) -N 2 "$exe" 2>/dev/null | tr -d '[:space:]')"
	fi

	case "${pe_sig:-}" in
		50450000)
			ps_desc="50450000 (正确)"
			;;
		*)
			ps_desc="${pe_sig:-<读取失败>} (无效!)"
			echo "✗【致命】PE 签名无效：期望 50450000，实际 '${pe_sig:-<读取失败>}'"
			echo "   e_lfanew 指向的位置不是 PE 头，基于它推算的偏移量不可信。"
			err=1
			;;
	esac

	case "${opt_magic:-}" in
		267)
			om_desc="267 = 0x10B (PE32)"
			;;
		523)
			om_desc="523 = 0x20B (PE32+)"
			;;
		*)
			om_desc="${opt_magic:-<读取失败>} (无效!)"
			echo "✗【致命】Optional Header Magic 无效：期望 267(0x10B) 或 523(0x20B)，实际 '${opt_magic:-<读取失败>}'"
			err=1
			;;
	esac

	echo "   体积      : $size 字节（下限 $min_size）"
	echo "   DOS 魔数  : $magic"
	echo "   e_lfanew  : ${lfanew:-<空>}"
	echo "   PE 签名   : $ps_desc"
	echo "   OptHeader : $om_desc"
	echo "   Subsystem : ${subsystem:-<读取失败>} $([ "$subsystem" = "2" ] && echo '(WINDOWS_GUI, 正确)' || echo '(非 GUI 子系统!)')"

	# 结构都不成立，就别再往下判 Subsystem / 体积了 —— 这个产物不认可
	if [ "$err" -ne 0 ]; then
		echo "   → PE 结构校验未通过，自检终止（不认可该产物）"
		return 1
	fi

	# ------------------------------------------------------------
	# 第二关：语义正确性（走到这里说明 PE 结构已确认无误）
	# ------------------------------------------------------------
	if [ "$subsystem" != "2" ]; then
		echo "✗【致命】Subsystem = ${subsystem:-?}，期望 2 (WINDOWS_GUI)"
		echo "   这是 CONSOLE 子系统：运行时会多弹一个控制台黑窗，"
		if [ "$label" = "GUI" ]; then
			echo "   而它与 GUI 主窗口同属一个进程 —— 关掉黑窗会把 GUI 一起杀掉。"
		fi
		echo "   修复：编译时务必带上 -ldflags \"-s -w -H windowsgui\""
		err=1
	fi

	if [ "$size" -lt "$min_size" ]; then
		echo "✗【致命】${label} 产物体积异常：$size < $min_size"
		echo "   体积远小于正常值，通常是编译时漏了东西。"
		if [ "$label" = "GUI" ]; then
			echo "   GUI 场景多为没链接上 rsrc_windows_amd64.syso —— 丢了 app.manifest，"
			echo "   会导致 walk 创建主窗口失败并静默退出（表现：双击没反应）。"
		fi
		echo "   修复：确认依赖产物已就绪后重新 build。"
		err=1
	fi

	return $err
}

echo "[1/5] 编译 BaiduPCS-Backup-GUI (GUI-Lite, 当前主版本)..."
cd "$LITE"
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
	go build -ldflags "-H windowsgui -s -w" -o BaiduPCS-Backup-GUI.exe .

echo "[2/5] 自检产物（GUI 子系统 / 已嵌 manifest）..."
if ! check_gui_exe "$OUT"; then
	echo
	echo "============================================================"
	echo " 构建终止：产物自检未通过，请勿分发上面这个 exe。"
	echo "============================================================"
	exit 1
fi
echo "   ✓ 自检通过"

echo "[3/5] 同步 GUI 与核心程序到免安装包..."
cp -f "$LITE/BaiduPCS-Backup-GUI.exe" "$PORT/BaiduPCS-Backup-GUI.exe"
cp -f "$CORE/BaiduPCS-Backup.exe"      "$PORT/BaiduPCS-Go/BaiduPCS-Backup.exe"
cp -f "$LITE/netdisk_logo.ico"         "$PORT/netdisk_logo.ico"

echo "[4/5] 清理免安装包内的本机状态 (账号/任务/日志)..."
rm -rf "$PORT/gui_config.json" "$PORT/mode_config.json" "$PORT/log" "$PORT/BaiduPCS-Go/log"
rm -f  "$PORT"/backup-task.*.json "$PORT"/BaiduPCS-Go/backup-task.*.json

echo "[5/5] 打包 BaiduPCS-Backup-Portable.zip..."
rm -f "$ROOT/BaiduPCS-Backup-Portable.zip"
"/c/Windows/System32/tar.exe" -a -c -f "$ROOT/BaiduPCS-Backup-Portable.zip" -C "$ROOT" BaiduPCS-Backup-Portable

echo "完成: $PORT"
ls -la "$PORT" "$ROOT/BaiduPCS-Backup-Portable.zip"

# [可选] 刷新已装目录: 仅在显式传入 INSTALL_DIR 时执行
if [ -n "$INSTALL_DIR" ] && [ -d "$INSTALL_DIR" ]; then
	echo "[附加] 刷新已装目录: $INSTALL_DIR"
	cp -f "$LITE/BaiduPCS-Backup-GUI.exe" "$INSTALL_DIR/BaiduPCS-Backup-GUI.exe"
	cp -f "$CORE/BaiduPCS-Backup.exe"      "$INSTALL_DIR/BaiduPCS-Go/BaiduPCS-Backup.exe"
	cp -f "$PORT/README.txt"               "$INSTALL_DIR/README.txt"
	echo "已装目录刷新完成 (gui_config.json / mode_config.json / log 保留不动)"
else
	echo "[附加] 跳过已装目录刷新 (未设置 INSTALL_DIR)"
fi
