# 04. 设备、任务与通信协议

协议定义设备、能力、任务和事件的业务语义。V1 传输层为 TLS/WSS；未来可替换为 HTTP/2、QUIC 或 P2P，但业务对象不变。

## 4.1 统一消息 Envelope

~~~json
{
  "protocol": "dsh-edge/1",
  "message_id": "msg_01J...",
  "kind": "task.submit",
  "sent_at": "2026-11-15T12:00:00Z",
  "correlation_id": "task_01J...",
  "sequence": 104,
  "ack": 103,
  "payload": {}
}
~~~

message_id 是单条消息稳定 ID；correlation_id 关联任务或请求；sequence 为发送方单连接递增序号；ack 是对端已确认的最高连续序号。

## 4.2 连接握手

1. client.hello：device_id、agent_version、protocol_versions、last_received_seq、last_event_cursor。
2. server.welcome：connection_id、server_time、selected_protocol、resume_from、heartbeat_interval。
3. device.register / device.registered：更新设备清单和能力 revision。
4. link.ready：开始正常任务和事件传输。

设备身份使用首次配对时生成的密钥对；短期连接凭证只用于建立连接，不替代设备身份。

## 4.3 Device Manifest

~~~json
{
  "device_id": "dev_01J...",
  "display_name": "build-pc",
  "platform": {"os": "windows", "arch": "x86_64", "version": "11"},
  "agent_version": "0.1.0",
  "labels": ["desktop", "gpu"],
  "resources": {"cpu_threads": 16, "memory_bytes": 34359738368, "disk_free_bytes": 500000000000},
  "capabilities_revision": 4
}
~~~

## 4.4 Capability Manifest

~~~json
{
  "capability_id": "process.exec",
  "version": "1.0",
  "description": "Run an approved process on this Worker",
  "input_schema": {"type": "object", "required": ["program", "args"]},
  "output_schema": {"type": "object"},
  "permission_scope": "process.exec",
  "resource_class": "cpu",
  "max_concurrency": 2,
  "discovery": "static"
}
~~~

能力状态分为 detected、available、authorized。探测到工具不代表已获准执行。

## 4.5 Task Envelope

~~~json
{
  "task_id": "task_01J...",
  "plan_id": "plan_01J...",
  "idempotency_key": "plan_01J...:task_01J...",
  "target_device_id": "dev_01J...",
  "capability": {"id": "process.exec", "version": ">=1.0 <2.0"},
  "input": {"program": "python", "args": ["-m", "pytest", "-q"], "cwd": "workspace/project"},
  "depends_on": [],
  "priority": "normal",
  "deadline": "2026-11-15T14:00:00Z",
  "replay_policy": "never",
  "requested_permissions": ["process.exec"]
}
~~~

## 4.6 Receipt 与状态

Receipt 示例：
~~~json
{"task_id":"task_01J...","device_id":"dev_01J...","status":"accepted","accepted_at":"2026-11-15T12:01:00Z","event_seq":0}
~~~

状态：planned、queued、dispatched、accepted、running、waiting_approval、succeeded、failed、cancelled、rejected、unknown。unknown 仅是 Controller 的观测状态。

Worker 在返回 accepted 前必须先将任务写入本地 ledger。相同 idempotency_key 再次到达时返回已有任务。

## 4.7 Event

~~~json
{
  "event_id": "evt_01J...",
  "task_id": "task_01J...",
  "device_id": "dev_01J...",
  "seq": 12,
  "type": "task.progress",
  "timestamp": "2026-11-15T12:03:00Z",
  "payload": {"message":"Tests running","completed":24,"total":80}
}
~~~

事件先写入 Worker 本地 outbox，再异步发送。Coordinator ACK 后 Worker 才能按保留策略清理已确认事件。Controller 按 device_id、task_id、seq 去重。

## 4.8 V1 消息类型

- link：client.hello、server.welcome、link.ready、link.ping、link.pong、link.ack、link.resume
- device：device.register、device.registered、device.heartbeat、device.capabilities.changed
- task：task.submit、task.accepted、task.rejected、task.cancel、task.snapshot、task.reconcile
- event：event.append、event.ack、event.replay
- approval：approval.request、approval.resolve
- artifact：artifact.create_upload、artifact.upload_progress、artifact.complete、artifact.download

## 4.9 网络与重放语义

- 控制消息按 at-least-once 交付；接收方按 message_id 去重。
- 任务按 idempotency_key 去重；网络超时不能直接创建新 task_id。
- 事件按 seq 续传；重复事件允许重放，由 Controller 去重。
- 心跳不承载业务数据；日志流有独立速率限制。
- 大文件不进入 WSS 消息，使用 Artifact HTTPS API。
- 未确认消息在本地持久化，重连后按 ACK 游标补发。

## 4.10 Artifact Ref

~~~json
{
  "artifact_id": "art_01J...",
  "task_id": "task_01J...",
  "name": "pytest-report.xml",
  "media_type": "application/xml",
  "size_bytes": 204800,
  "sha256": "...",
  "download_uri": "/v1/artifacts/art_01J..."
}
~~~

Artifact 使用分块上传，服务端记录已确认 offset。默认分块 4 MiB，可配置；完成后校验总大小和 SHA-256。
