# BaiduPCS-Backup

基于 [BaiduPCS-Go](https://github.com/qjfoidnh/BaiduPCS-Go) 二次开发的 Windows 定时静默备份工具。

## 目录结构

- `BaiduPCS-Go/` 核心程序（二次开发）：新增 `backup run` 命令，支持智能/普通/无感三种性能模式，输出全部写入日志文件（无控制台窗口）
- `BaiduPCS-Backup-GUI-Lite/` 图形界面（walk）：主界面触发功能、任务管理、设置（账号管理/基础设置）、日志输出
- `ICON/` 图标资源

## 编译

核心（GUI 子系统，静默无黑框）：

```
cd BaiduPCS-Go
go build -ldflags "-s -w -H windowsgui" -o BaiduPCS-Backup.exe .
```

GUI：

`BaiduPCS-Backup-GUI-Lite/` 使用 [lxn/walk](https://github.com/lxn/walk)，依赖 Windows Common Controls v6 manifest。
本仓库已将所需资源文件入库，**直接编译即可运行**；文件说明与重新生成方法见下方「GUI 构建前置」。

```
cd BaiduPCS-Backup-GUI-Lite
go build -ldflags "-s -w -H windowsgui" -o BaiduPCS-Backup-GUI.exe .
```

### GUI 构建前置（重要）

GUI 依赖 [lxn/walk](https://github.com/lxn/walk)，而 walk **必须**由 Windows Common Controls v6 manifest 启用。
缺少该资源时 `go build` 不会产生任何报错（退出码 0、零警告），但运行时会静默退出
（`walk.NewMainWindow` 报 `TTM_ADDTOOL failed`），表现为"双击无反应"。

本仓库已将下列资源文件入库，**clone 后直接编译即可运行**：

- `BaiduPCS-Backup-GUI-Lite/app.manifest` —— manifest 源文件（Common Controls 6.0 + `asInvoker` + DPI 感知）
- `BaiduPCS-Backup-GUI-Lite/rsrc_windows_amd64.syso` —— 由上述 manifest 与 `netdisk_logo.ico` 经 `rsrc` 生成的资源节

> **10 秒自检**：编译产物 `BaiduPCS-Backup-GUI.exe` 约 **7.50 MB** 表示资源已注入，可正常运行；
> 若只有约 **7.34 MB**，说明资源节为空（RVA=0），双击必定无反应。

如需更换图标或修改 manifest，用 `rsrc` 重新生成：

```
go install github.com/akavel/rsrc@latest
cd BaiduPCS-Backup-GUI-Lite
rsrc -manifest app.manifest -ico netdisk_logo.ico -o rsrc_windows_amd64.syso
go build -ldflags "-s -w -H windowsgui" -o BaiduPCS-Backup-GUI.exe .
```

## 功能特性

- 计划任务定时静默备份（任务统一存放于 任务计划程序\Microsoft\BaiduPCS-Go）
- 智能模式：按系统负载采样动态调整并发数与带宽；普通模式：固定参数；无感模式：单任务 200KB/s
- 多账号管理（BDUSS / Cookies 登录）
