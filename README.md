# Paily Check — Paily ON NAS

> **Paily ON NAS 自托管平台的节点测活服务。**
> 从主仓 `paily-core` 取节点，先初筛再复筛（延迟 / 抖动 / 出口地区 / 流媒体解锁 / 可选测速），把结果上报回主服务。

本仓库是 [Paily ON NAS](https://github.com/Eiyuuzaki/Paily-ON-NAS) 的测活组件，基于 Paily Connect 已开源的 `paily-check` 继续维护（AGPL-3.0）。仅供个人学习自用，勿对外提供服务。

![Platform](https://img.shields.io/badge/platform-linux%2Farm64-blue) ![Go](https://img.shields.io/badge/Go-1.24%2B-00ADD8) ![License](https://img.shields.io/badge/license-AGPL--3.0-orange)

## 在 Paily ON NAS 中的位置

```text
   Paily 主服务 (paily-core)     源库 · 节点池
        │  ↓ 取节点
   Paily 测活 (paily-check)      初筛 · 复筛
        │  ↑ 上报结果
   Paily 主服务 (paily-core)     存活判定 · 评分 · 分发
```

## 本分支相对上游的改动

- **IPv4 兜底拨号**：节点 raw 拨号地址是 IPv6、但 `server` 列有 IPv4 时，改用 IPv4 探测——与分发给客户端的地址一致，避免 Cloudflare v6 节点被误判死。
- **多目标探测**：非 CF 节点的探测目标按 `Cloudflare(204) → Apple → gstatic` 依次兜底，任一成功即判活，避免夜间单一探测目标被封导致误杀。
- **出口地区以实测为准**：配合主仓用实测出口 IP 回填地区，避免用入口猜测误标国家。

其余用法与原仓库一致，见下方原仓库 README。

---

## 原仓库 README（Paily Connect 上游 `paily-check`）


> [!IMPORTANT]
> **本仓库为 Paily Connect 开源仓库的一部分**
>
> 关于 Paily Connect 开源仓库的更多信息，请查看 [此页面](https://github.com/openpaily/paily-core) 。
> 
> Paily Connect 系列是我们的内部项目，其仍处于早期开发阶段。
> **我们极度不建议您直接部署此版本**。该版本可能会有潜在的未知问题并可能造成数据丢失等异常，我们建议您等待可用的分支版本部署。

Paily 测活后端（Paily Check）是 Paily Connect 平台的节点测活服务。它从 **Paily 主服务**（`paily-core`）获取节点，先做初筛判断是否存活，再对存活节点做复筛，测量详细延迟、抖动、出口地区、流媒体解锁情况和可选下载速度，最后把结果上报。

测活结果由 Paily 主服务写入节点池，用于存活判定、评分和分发。本服务只负责采集数据，不保存节点，也不参与分发。

## 在平台中的位置

```text
   Paily 主服务          源库 · 节点池
        │
        ▼
   Paily 测活后端          初筛 · 复筛
        │
        ▼
   Paily 主服务          存活判定 · 评分 · 分发
```

## 工作流程

每轮测活在 Cron 触发后按以下阶段进行：

1. **初筛**：对所有节点做少量 Ping，得到延迟。延迟为 `-1` 或超过阈值的节点不再进入后续检测。
2. **复筛**：对存活节点依次执行：
   - **详细延迟**：多次 Ping，记录平均延迟和抖动。
   - **出口地区**：检测节点的实际出口国家或地区。
   - **流媒体脚本**：运行解锁检测脚本。
   - **下载测速**（可选）：测量下载速度。
3. **上报**：把每个节点的初筛结果和复筛结果提交给 Paily 主服务。

每个阶段独立运行并输出吞吐指标，便于观察各阶段的耗时与速率。复筛各阶段的详细参数见 [测活流程](docs/check-pipeline.md)。

## 运行

```sh
cp config.yaml.example config.yaml
export PAILY_CORE_URL=https://core.example.com
export PAILY_SERVICE_SECRET=replace-with-a-secret
go run ./cmd/checker --config config.yaml
```

环境变量优先于 `config.yaml`。完整配置项及默认值见 [`config.yaml.example`](config.yaml.example)。

## 结果上报

每个节点上报一条结果，包含初筛存在的延迟，以及复筛得到的平均延迟、抖动、出口地区、流媒体解锁结果和可选速度。未通过初筛的节点只上报初筛结果。

Paily 主服务如何使用这些结果，见其 `docs/node-pool.md` 与 `docs/scoring.md`。

## 脚本

Paily 测活后端内置基于脚本的解锁检测能力，不内置具体检测脚本。可自行编写 JS 脚本或 Native Handler，详见 [脚本开发](docs/scripting.md)。

## 定时与并发

测活由 `schedule.cron`（六字段 Cron 表达式）触发，启动时也会立即执行一次。若上一轮尚未结束，新的触发会被跳过。各阶段的并发数可分别配置。

## Docker

```sh
export PAILY_CORE_URL=https://core.example.com
export PAILY_SERVICE_SECRET=replace-with-a-secret
docker compose up --build -d
```

## 鸣谢
本项目使用了大量 [miaospeed](https://github.com/miaokobot/miaospeed) 的代码，使用了基于其定制的测试引擎和脚本运行时。

## 许可证

本项目基于 GNU Affero General Public License v3.0（AGPL-3.0）发布，详见 [LICENSE](LICENSE)。
