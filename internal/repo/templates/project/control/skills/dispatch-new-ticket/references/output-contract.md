# Output Contract

本 skill 不写文件化 receipt。

dispatch 的权威输出在数据库字段 `pm_dispatch_jobs.result_json`，结构是：

- `dalek.pm_dispatch_job_result.v1`
- 包含 `schema` 与 `injected_cmd`（可附带 worker_loop 摘要字段）

约束：

1. skill 只负责准备 worker 上下文文件。
2. dispatch 完成结果由 Go 在 DB 中统一记录。
