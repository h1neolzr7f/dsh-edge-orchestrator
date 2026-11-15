# 07. 验收与测试计划

## 7.1 最小环境

- Controller：Android DSH Go。
- Coordinator：Windows PC 或 Linux 小主机；另测 VPS。
- Worker A：Windows 10/11。
- Worker B：Linux x86_64 或 ARM64。
- 网络：同 LAN、跨网络、弱网模拟。

## 7.2 第一关键验收：真实多设备并行

目标：“A 电脑运行测试，B 电脑审查 diff，最后汇总。”

通过条件：
1. 两台 Worker 均返回 accepted。
2. 两个任务执行时间窗口重叠。
3. 各自生成结果与 Artifact。
4. Coordinator 汇总结果。
5. 手机断网再恢复，仍显示同一 plan_id/task_id 和最终状态。

## 7.3 连接与弱网验收

测试 1–3% 丢包、200–800ms RTT、10–60 秒断网、Wi-Fi/蜂窝切换、Coordinator 重启、Worker 休眠恢复和多 Worker 同时重连。

检查：
- 连接状态正确区分 online/degraded/offline；
- 未确认消息按游标续传；
- 同一任务不会因重复消息启动两次；
- 一个 Worker 慢连接不阻塞其他节点；
- Worker 断线时已接受任务继续执行。

## 7.4 Artifact 续传

使用 100MB 和 1GB 文件：
- 上传 50% 时断网；
- 恢复后从服务端确认 offset 续传；
- 最终 SHA-256 与源文件一致；
- 文件传输期间控制消息仍能低延迟往返。

## 7.5 能力自主探索

- 新 Worker 安装后自动上报 OS、架构、CPU、内存和工具清单。
- 安装/卸载 Git、Python、ADB 后，能力 revision 变化。
- 探测结果区分 detected、available、authorized。
- 用户授权后能力进入可调度状态。
- 调度器只选择满足 capability schema 和权限的节点。

## 7.6 任务幂等与恢复

- Worker 接受任务后，在 Receipt 返回前断网。
- Controller 将任务视为 unknown。
- 恢复后使用原 task_id 查询。
- Worker 返回原任务状态，不创建第二个执行实例。
- Worker 重启后读取本地 ledger，恢复或明确标记无法确认的任务。

## 7.7 初始性能目标

以下是第一轮工程目标，必须用真实设备和网络数据修订：
- LAN 设备注册到在线：P95 小于 2 秒。
- 正常网络任务 submit 到 accepted：P95 小于 3 秒，不含模型规划。
- 断网恢复后连接重建：目标小于 10 秒。
- 两个任务派发时间间隔：小于 2 秒。
- 正常网络事件更新：P95 小于 2 秒。

## 7.8 发布验收材料

- 环境与版本信息；
- 网络条件记录；
- plan/task IDs；
- 两台 Worker 的开始/结束时间；
- Coordinator 连接与 ACK 日志；
- Artifact SHA-256；
- 断网与恢复日志或录像；
- 弱网测试结果和已知限制。
