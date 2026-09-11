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

```
cd BaiduPCS-Backup-GUI-Lite
go build -ldflags "-s -w -H windowsgui" -o BaiduPCS-Backup-GUI.exe .
```

## 功能特性

- 计划任务定时静默备份（任务统一存放于 任务计划程序\Microsoft\BaiduPCS-Go）
- 智能模式：按系统负载采样动态调整并发数与带宽；普通模式：固定参数；无感模式：单任务 200KB/s
- 多账号管理（BDUSS / Cookies 登录）
