# Dalek Web Console Design

## 目标

为 dalek 提供一个最小可用的 web 管理页面，实现从后端运行态到前端可视化与基础操作的闭环。

## 设计原则

- 先打通真实链路，再补完体验细节
- 优先复用现有 app/service facade，而不是绕过架构直接读 DB
- PM 验收依赖真实场景，因此页面必须可启动、可访问、可操作

## 模块拆分

### 1. 后端读模型 / API
- dashboard 聚合接口
- ticket list / detail 接口
- merge / inbox 查询接口
- planner / runtime 状态接口

### 2. 前端页面
- App Shell / 导航
- Overview 页面
- Tickets 页面
- Merge / Inbox 页面
- Planner / Runtime 页面

### 3. PM 状态与验收面
- `.dalek/pm/plan.md`：语义状态
- `.dalek/pm/state.json`：结构化运行态
- `.dalek/pm/acceptance.md`：真实验收 evidence

## 数据流

1. 页面请求后端 facade API
2. API 从现有 app/service 层读取 ticket / worker / merge / inbox / planner 状态
3. 前端按页面模型渲染
4. PM 通过真实浏览器场景验证页面是否满足需求

## 风险

- 后端已有数据模型偏 CLI/TUI 视角，可能需要额外聚合接口
- 前端页面容易停留在“静态展示”，必须确保至少一个真实操作链路
- PM 验收若仍退化成 `go test`，会偏离目标

## 真实验收方案

必须至少完成以下流程：
- 启动真实 dalek 服务
- 打开 web 管理页面
- 查看 overview / tickets / planner / merge-inbox 页面
- 完成一个真实操作链路
- 记录 URL、步骤、结果与结论到 acceptance evidence
