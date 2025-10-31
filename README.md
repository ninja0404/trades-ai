# Trades-AI

面向加密货币合约的自动化交易系统，结合技术指标特征、大语言模型策略决策与风险管理，支持以 Hyperliquid 为执行端（默认连接其测试网），同时使用 Binance USDⓈ-M 行情获取 1 小时 / 4 小时 K 线与盘口数据。

## 功能概览

- **市场数据采集**：通过 CCXT 抓取 Binance 合约端 1H / 4H K 线与深度盘口，统一封装为快照。
- **指标与特征提取**：对多时间框架数据计算 EMA、MACD、RSI、ATR、量能、市场结构等特征，用于辅助策略决策。
- **AI 决策引擎**：调用 OpenAI 模型（默认 GPT-4.1），基于特征和当前仓位得到结构化的操作建议、止损/止盈与风险提示。
- **风险控制**：评估账户权益、信心阈值、可承受风险、日度止损等，输出目标仓位、最大风险金额以及最终执行依据。
- **执行层**：根据目标仓位生成市场主单，并可附带止损/止盈触发单，通过 Hyperliquid 接口实际下达。
- **监控与审计**：所有核心事件（行情、决策、风险评估、订单执行、异常等）写入 SQLite，用于事后观察与审计。

## 系统流程

```
行情 (Binance USDⓈ-M) ➜ 特征提取 ➜ AI 决策
                                ↓
          仓位 / 资金状态 (Hyperliquid) ➜ 风险评估 ➜ 执行计划 ➜ 下单 (Hyperliquid)
                                                        ↓
                                                  监控与持久化
```

### 模块说明

| 模块 | 位置 | 职责 |
| ---- | ---- | ---- |
| `internal/exchange` | 行情客户端与快照聚合 | 拉取 1H / 4H K 线、盘口快照，封装 `MarketSnapshot` |
| `internal/indicator` & `internal/feature` | 指标与特征 | 计算 EMA、MACD、RSI、ATR、量能、市场结构等特征集合 |
| `internal/ai` | 大模型调用 | 生成决策 JSON，包括仓位建议、信心、止损/止盈等 |
| `internal/risk` | 风险控制 | 结合账户权益、风险参数、AI 建议，输出目标仓位与执行许可 |
| `internal/execution` | 执行计划与下单 | 生成主单及触发单，调用 Hyperliquid 下达，处理重试 |
| `internal/position` | 仓位管理 | 查询 Hyperliquid 余额与持仓，解析账户摘要 |
| `internal/monitor` | 事件记录 | 持久化行情、决策、风险、执行等关键数据到 SQLite |
| `internal/backtest` | 模拟组件 | 目前用于指标复用，若后续扩展回测可直接调用 |

## AI 决策输出格式

模型需要按固定结构返回决策 JSON，核心字段如下：

```json
{
  "intent": "OPEN|ADJUST|CLOSE|HEDGE",
  "direction": "LONG|SHORT|FLAT|AUTO",
  "target_exposure_pct": 0.0,
  "adjustment_pct": 0.0,
  "confidence": 0.0,
  "reasoning": "...",
  "order_preference": "MARKET|LIMIT|AUTO",
  "new_stop_loss": "...",
  "new_take_profit": "...",
  "risk_comment": "..."
}
```

- **intent**：本次操作意图，`CLOSE` 表示直接将仓位降为 0；`HEDGE` 可用于反向操作或对冲。
- **direction**：目标方向；`AUTO` 允许根据现有仓位或分析自行判断，`FLAT` 强制回到空仓。
- **target_exposure_pct**：期望的仓位绝对占比（0~1），例如 `0.25` 表示 25% 净值；若 intent=CLOSE 请填 0。
- **adjustment_pct**：在当前仓位基础上的相对调整幅度，可选；正值表示增加仓位，负值表示减仓，未调整填 0。
- **confidence**：当前结论的可靠度，0~1 之间；风险层会根据信心阈值调节可用风险额度。
- **order_preference**：下单方式偏好（默认 `AUTO`）。
- **new_stop_loss / new_take_profit**：新的止损/止盈价格，必须为可解析的数值字符串。
- **risk_comment**：任何额外的风险提示，用于帮助风控和监控记录。

风险层会基于上述字段、账户参数以及最新行情重新评估目标仓位，必要时对目标进行裁剪或拒绝执行。执行层会将 `target_exposure_pct` 转换成具体手数，并根据 `order_preference` 和止损/止盈信息构造主单与保护单。 

## 环境要求

- Go 1.21+（建议与 `go.mod` 保持一致）
- 可访问外网的运行环境（需要调用 Binance、Hyperliquid、OpenAI）
- SQLite3（内置驱动 `github.com/mattn/go-sqlite3`）

## 快速开始

1. **克隆仓库并安装依赖**

```bash
git clone https://github.com/your-org/trades-ai.git
cd trades-ai
go mod tidy
```

2. **配置密钥**

编辑 `configs/config.yaml`，需要关注的关键段说明如下：

```yaml
exchange:
  name: binanceusdm          # 行情端，保持为 binanceusdm
  market: BTC/USDT:USDT      # Binance USDⓈ-M 合约标的
  api_key: ""                 # 行情仅使用公共接口，可留空
  api_secret: ""
  api_password: ""

trade_exchange:
  name: hyperliquid
  market: BTC/USDC           # Hyperliquid 永续标的
  api_key: ""                 # 可选：若使用 API Key 鉴权
  api_secret: ""
  api_password: ""
  wallet_address: "0x..."     # 必填：Hyperliquid API 钱包地址
  private_key: ""             # 必填：钱包私钥（十六进制）

openai:
  api_key: "sk-..."           # 必填：OpenAI API Key
  base_url: https://api.openai.com/v1
  model: gpt-4.1              # 可按需调整模型名称
  timeout: 15s

risk:
  max_trade_risk: 0.01        # 单笔最大风险（占净值）
  max_daily_loss: 0.03        # 当日最大亏损
  max_exposure: 0.2           # 总仓位上限
  confidence_full_risk: 0.8   # AI 信心达到该阈值使用满仓位风险
  confidence_half_risk: 0.6   # AI 信心达到该阈值使用半仓位风险
  enable_daily_stop_loss: true

execution:
  slippage: 0.01              # 市价单滑点系数（用于 Hyperliquid）

scheduler:
  loop_interval: 5m           # 调度循环间隔
  decision_interval: 1h       # 最短决策间隔（主周期）
  trend_interval: 4h          # 趋势过滤参考周期
```

> **安全提示**：请妥善保管 Hyperliquid 钱包私钥与 OpenAI API Key，可通过环境变量或密钥管理服务注入，避免硬编码在仓库中。

3. **初始化数据库**

默认使用 `data/trades_ai.db` 作为 SQLite 存储，可根据需要调整 `database` 配置段；首次运行时会自动创建监控与风险表结构。

4. **运行程序**

```bash
# 使用默认配置路径
go run ./cmd/trader

# 或显式指定配置文件
go run ./cmd/trader -config configs/config.yaml
```

程序启动后会：
- 首次执行 `Tick`，随即按 `loop_interval` 周期运行；
- 每隔 `decision_interval` 触发一次完整决策流程；
- 将事件记录写入 SQLite，便于追踪。

## 监控数据结构

`internal/monitor` 会把关键事件写入 `monitor_events`：
- `market_snapshot`：特征集合与时间戳；
- `ai_decision`：模型决策原文及解析结果；
- `risk_evaluation`：输入、评估结果、风控备注；
- `execution`：执行计划、订单参数与结果；
- `position`：账户余额、持仓详情；
- `error`：异常消息与上下文。

可使用 SQLite 工具查看，例如：

```bash
sqlite3 data/trades_ai.db "SELECT event_type, created_at FROM monitor_events ORDER BY id DESC LIMIT 20;"
```

## 常见问题

- **为何仍保留 Binance 配置？** 行情模块目前依赖 Binance USDⓈ-M 的公共 API 获取多时间框架 K 线与盘口；执行端与仓位查询已全部指向 Hyperliquid。
- **是否支持 Hyperliquid 测试网？** 支持。只需在 `trade_exchange` 中设置测试网钱包与私钥（并在 CCXT 中开启 `UseSandbox`，可通过配置 `trade_exchange.use_sandbox`）。
- **模型输出异常怎么办？** 默认为严格的 JSON 验证，无法解析时会记录错误并跳过执行，可在监控事件中查看原始响应。

## 目录结构速览

```
cmd/trader/           # 程序入口，负责加载配置与启动 orchestrator
internal/app/         # orchestrator 主流程
internal/exchange/    # 行情客户端与快照服务
internal/feature/     # 特征提取器
internal/ai/          # OpenAI 封装与决策解析
internal/risk/        # 风险控制逻辑
internal/execution/   # 执行计划构建与下单
internal/position/    # 仓位管理
internal/monitor/     # 事件记录
internal/backtest/    # 模拟器及相关接口（供未来回测扩展）
internal/config/      # 配置解析与校验
internal/log/         # 日志初始化
internal/store/       # SQLite 封装
```

## 后续可扩展方向

- 将行情来源切换/扩充为 Hyperliquid 或其他交易所，统一多源数据；
- 引入更多 AI 策略或提供多模型投票机制；
- 在 `internal/backtest` 基础上完善历史回测；
- 增加 REST / Web UI 用于查看监控与决策记录；
- 针对止损/止盈执行增加更精细的触发策略（如分批退出、追踪止损）。

如有建议或想要贡献，欢迎提交 Issue 或 PR。
