# context-LLM

[English](README.en.md) | 中文

一个无第三方 Go 依赖的长上下文测试工具，用于评估 OpenAI 兼容接口。

当前版本：`1.2.5`

主要能力：

- 按 K/M 档位测试上下文容量
- 精确检索、多跳推理、规则冲突和对抗干扰题型
- 普通模式与增强自动测试
- 自动 token 校准、输出余量扩展、错误重试和断点续跑
- 分题型评分、重复测试统计和完整响应元数据
- 可配置二分精度、并发执行和全局请求限流
- 本地 Markdown 预览、自动打开浏览器和 5 分钟超时
- 跨 macOS、Windows、Linux 的 x64/ARM64 构建

## 快速开始

首次运行时，如果可执行文件同目录没有 `config.txt`，程序会生成默认配置并退出。编辑配置后再次运行。

```sh
./build.sh
```

构建后，`dist/` 中会生成当前平台对应的可执行文件。下面用 `<binary>` 代替实际文件名。

至少替换三项：

```ini
base_url = 你的接口地址
model = 你的模型 ID
api_key = 你的 API 密钥
```

先检查配置，不发送请求：

```sh
./<binary> --dry-run
```

正式运行：

```sh
./<binary>
```

也可以从源码指定配置运行：

```sh
go run . --config ./config.txt
```

常用命令速查：

| 目的 | 命令 |
|---|---|
| 检查配置 | `./<binary> --dry-run` |
| 运行测试 | `./<binary>` |
| 打开最新报告 | `./<binary> --preview latest` |
| 恢复中断测试 | `./<binary> --resume latest` |
| 完整验证 | `make verify` |

## 报告预览

打开最近一次测试报告：

```sh
./<binary> --preview latest
```

也可以指定报告文件：

```sh
./<binary> --preview path/to/RESULTS.md
```

程序只监听本机 `127.0.0.1`，会尽力自动打开浏览器。没有浏览器或图形环境时，会打印 Markdown 文件路径供直接查看；存在浏览器时可按 `Ctrl+C` 提前停止，服务最多运行 5 分钟后自动关闭。

将 `preview_after_run = true` 写入配置后，正常测试结束时也会自动进入该预览流程。

## 配置说明

配置文件使用中文注释：独立行以 `#` 开头，行尾 `#` 后的内容也会被忽略。下表列出程序首次生成时的默认值；已有配置中的自定义值不会被覆盖。

### 服务商连接

| 配置 | 默认值 | 说明 |
|---|---|---|
| `base_url` | `https://api.openai.com/v1` | OpenAI 兼容接口地址 |
| `model` | `GPT6.1-Astra` | 被测模型 ID |
| `api_key` | 占位密钥 | 正式运行前必须替换 |
| `api_mode` | `chat-completions` | 请求格式，可选 `chat-completions` 或 `responses` |
| `reasoning_effort` | `default` | `default` 不发送；可选 `none/minimal/low/medium/high/xhigh/max` |
| `reasoning_summary` | `default` | 仅 Responses；`default` 不发送，可选 `auto/concise/detailed` |

### 上下文与输出

| 配置 | 默认值 | 说明 |
|---|---|---|
| `tiers` | `200K, 400K, 600K, 800K, 1M` | 测试档位；支持纯数字、K、M 和小数 |
| `chars_per_token` | `6.25` | 初始输入估算，后续按实际用量校准 |
| `max_output_tokens` | `2500` | 最小输出预算，程序会按锚点数自动扩展 |
| `timeout_seconds` | `900` | 单次请求超时时间 |
| `transport_retries` | `1` | 网络、408、普通 429、可恢复 5xx 的额外重试次数 |

`tiers` 示例：

```ini
tiers = 20K, 1.1M, 2M
```

内部会统一转换为整数 token，并校验递增、唯一且大于 12K。

### 自动测试

| 配置 | 默认值 | 说明 |
|---|---|---|
| `auto_calibrate` | `true` | 根据实际输入 token 校准后续档位 |
| `enhanced_auto_test` | `false` | 开启后每档使用新题测试三次，并自动二分 |
| `binary_precision_tokens` | `10000` | 增强模式二分搜索精度，单位 token；越小越精确 |
| `structured_output` | `true` | 优先发送 JSON 结构约束，不支持时自动降级 |
| `parallel_tests` | `false` | 仅增强模式生效；并行执行同档三次测试 |
| `request_interval_ms` | `0` | 全局请求起始间隔；0 表示不限速 |

普通模式保留单次测试和实际 token 校准；增强模式固定每档三次，每次生成新的题目，并以三次输入 token 的中位数作为校准依据。

### 题目与评分

| 配置 | 默认值 | 说明 |
|---|---|---|
| `anchor_count` | `13` | 总题目数，范围 13-50 |
| `evidence_tolerance` | `0` | 宽容的证据错误数；验证答案后允许缺少证据，弱模型建议不超过 2 |
| `test_modes` | 四类全开 | 逗号分隔的题型开关 |

可用题型：

| 模式 | 能力 |
|---|---|
| `needle` | 精确定位分散信息 |
| `multi_hop` | 跨记录组合推理 |
| `conflict` | 新旧规则冲突消解 |
| `adversarial` | 相似错误信息干扰 |

无论启用哪些题型，都会保留一题“材料不足”拒答题。

### 输出与预览

| 配置 | 默认值 | 说明 |
|---|---|---|
| `report_language` | `en` | `RESULTS.md` 语言；可选 `en` 或 `cn` |
| `preview_after_run` | `false` | 测试完成后自动打开报告预览 |

## 测试流程

### 普通模式

1. 按 `tiers` 从小到大生成随机材料和题目。
2. 每档执行一次完整测试。
3. 遇到 `context_too_large` 后，使用新题复测前一档。
4. 使用该次实际 `prompt_tokens` 校准后续档位。

### 增强模式

将 `enhanced_auto_test` 设为 `true` 后，每次运行都会重新生成题库：

1. 每档固定测试三次，每次生成新的随机题库。
2. 使用三次输入 token 的中位数校准。
3. 遇到失败档位后，在通过档与失败档之间按 `binary_precision_tokens` 指定的精度自动二分，默认 10000 token。
4. 如果所有配置档位都通过，会继续向上探测上下限。

### 请求可靠性

- `transport_retries` 控制网络中断、HTTP `408`、普通 `429` 和可恢复 `5xx` 的额外重试次数。
- `model_cooldown` 使用更长的初始等待和最高 5 分钟退避；普通限流最高 30 秒。
- 支持 `Retry-After`，冷却场景可遵守更长的等待时间。
- 单纯 `usage_limit_reached` 不会盲目重试，直接保留错误报告。
- 上下文超长、鉴权错误和普通参数错误不会重试。
- `structured_output` 不被供应商支持时会自动降级，并记录 fallback。
- JSON 输出可自动提取和修复代码块、尾逗号、缺失结尾等常见问题。
- `max_output_tokens` 是最小预算；若返回 `finish_reason=length`，会自动扩容一次。

## Token 用量

启动测试时会按档位显示预估用量，例如：

```text
Estimated usage for 200000: input 189.5K, output 2.5K, total 192.0K
```

测试完成后，`RESULTS.md` 会显示：

- 每次实际输入、输出和总 token
- 每档实际总量与平均总用量
- 输入 token 均值、中位数和范围
- 通过率、上下限次数、失败次数、平均耗时、耗时中位数和 P95
- 每种题型的答案与证据成绩
- 实际返回模型、finish reason、cached/reasoning tokens
- 结构化输出是否降级，以及 JSON 是否自动修复
- 下次运行建议的 `chars_per_token`（增强模式取三次校准值的中位数）

将报告中的建议值写回配置中的 `chars_per_token`，可以减少下一档位的输入 token 估算偏差。

## 结果文件

每次运行保存在：

```text
runs/run_日期_时间/
├── RESULTS.md
├── summary.json
├── tier_summary.json
├── run_config.public.json
└── checkpoint.json
```

测试成功后会删除大型材料、私有答案和原始响应。响应正文不会重复保存，但请求 ID、模型、状态、finish reason、usage、重试和输出扩容等元数据会保留在 `summary.json` 与 checkpoint 中。

`RESULTS.md` 的语言由 `report_language` 控制：`en` 输出英文，`cn` 输出中文。报告包含 `Errors` 表，直接显示失败类型、HTTP 状态、错误代码、供应商消息和重试次数。

中文报告会翻译常见错误代码和常见供应商提示；无法可靠识别的英文消息会保留原文，完整原始错误仍保存在 `summary.json`。

`run_config.public.json` 不包含 API 密钥。

## 断点续跑

恢复最近一个未完成运行：

```sh
./<binary> --resume latest
```

指定运行目录：

```sh
./<binary> --resume runs/run_20261108_120000
```

恢复时会校验配置指纹。每次 trial 完成后都会更新 checkpoint；恢复时会复用已完成的单次测试，只补跑尚未完成的 trial。档位、探测阶段、二分边界、已完成尝试次数和响应元数据也会保留。

## 常规验证

完整验证包括格式检查、`go vet` 和竞态检测：

```sh
make verify
```

等价于依次执行：

```sh
gofmt -l main.go internal
go vet ./...
go test -race ./...
```

仅运行普通测试：

```sh
make test
```

## 跨平台构建

无参数时自动识别当前系统，只构建对应版本：

```sh
./build.sh
# 或
make build
```

指定平台：

```sh
./build.sh windows-arm64
./build.sh linux-x64
```

构建全部平台：

```sh
./build.sh all
# 或
make build-all
```

支持目标：

- `macos-x64`
- `macos-arm64`
- `windows-x64`
- `windows-arm64`
- `linux-x64`
- `linux-arm64`

## GitHub Packages

项目会发布多架构容器镜像到 GitHub Packages：

```text
ghcr.io/recaeent/context-llm
```

拉取当前版本：

```sh
docker pull ghcr.io/recaeent/context-llm:v1.2.5
```

使用已准备好的配置运行：

```sh
docker run --rm \
  -v "$PWD/config.txt:/app/config.txt:ro" \
  ghcr.io/recaeent/context-llm:v1.2.5
```

推送 `v*` 标签后，workflow 会自动构建 `linux/amd64` 和 `linux/arm64` 镜像，并发布 `v*` 与 `latest` 标签。

## API 密钥安全

- API 密钥只从 `config.txt` 读取。
- 默认 `sk-123456789` 是占位符，正式运行前必须替换。
- 程序不会修改已有配置文件的权限，请按操作系统管理文件访问权限。
- 真实密钥不会写入公开配置、checkpoint、Markdown 报告或原始结果。
- 如果配置文件中保存了真实密钥，启动时会提示不要分享该文件。

## License

本项目使用 [The Unlicense](LICENSE)，将软件放入公共领域，允许任何人自由使用、修改和再发布。


## 项目结构

```text
main.go                     程序入口与运行编排
internal/config             配置解析与验证
internal/corpus             题型、题库与长材料生成
internal/provider           API 请求、响应解析与元数据
internal/scoring            答案、证据和 JSON 修复评分
internal/runner             测试执行、重试、校准、并发与恢复
internal/report             统计汇总与 Markdown 报告
internal/mdrender           受限 Markdown 转安全 HTML
internal/preview            本地报告服务与浏览器打开
build.sh                    跨平台构建脚本
Makefile                    验证与构建入口
Dockerfile                  GHCR 多架构镜像定义
```
