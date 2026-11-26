# configcheck 与「注释即可选键」

> 状态：**已知问题，暂不修复**（2026-11-26）
> 影响面：`internal/configcheck`（`weld config check`）
> 相关模板：`internal/weldtemplate/capabilities/*/files/config.snippet`
> 关联讨论：`weld upgrade` 把已注释的可选键当成未知设置误报

## TL;DR

`configcheck` 把 `config.example.yml` 当作机器 schema，但 weld 自己的模板**故意把可选键写成注释**。YAML 解析会丢掉注释，比较器看不到这些键，于是用户正常设置一个**文档里出现过、Go 结构体里也存在**的键，反而被告警「不是已知设置」。这不是解析错误，而是「注释即代表可选」这一约定从未被检查器建模。

## 背景：configcheck 做什么

`weld config check`（`internal/cli/configcheck.go`）把 `config.yml` 与提交的 `config.example.yml` 逐键比对，**不执行项目、不读取值**，只产出三类 finding：

| Level | 触发 | 是否影响退出码 |
| --- | --- | --- |
| `fail` | 值类型与 example 不符、同一 mapping 里键重复 | 是（默认即失败） |
| `warn` | 键在 example 中不存在：`is not a known setting`（`configcheck.go:183`） | 仅 `--strict` |
| `warn` | example 中有、config 中没有：`is missing; the default or the environment applies`（`configcheck.go:190`） | 仅 `--strict` |

比较逻辑在 `mapping()`（`configcheck.go:163`）：遍历 `got`（config）的键，不在 `ref`（example）里就报 unknown；再遍历 `ref` 的键，不在 `got` 里就报 missing。

## 问题

example 里的键承担了两个互相冲突的角色：

- **出现的键** = schema：既「已知」，又「必须出现」，否则报 missing；
- **注释掉的键** = 文档：既「已知」，又「可以缺省」，既不报 unknown，也不该报 missing。

weld 的模板用注释表达「可选」，正是为了躲开 missing 噪音。代价是解析后这些键在 AST 里不存在，unknown 检查把它们当成不认识的键。

### 实例

模板（`internal/weldtemplate/capabilities/db/files/config.snippet`，26–31 行）：

```yaml
database_test:
  name: ""
  # host: ""
  # port: 0
  # user: ""
  # password: ""
  # dsn: ""
  # schema_only: false
```

项目在 `config.yml` 里写 `database_test.host: db.internal`（这是完全合法、`Database` 结构体里存在的字段），检查器却报：

```
warn database_test.host is not a known setting; the loader ignores it
     (a typo, or a setting missing from config.example.yml)
```

go-log2ban 里的实际实例是 `log2ban.enabled`：`config.example.yml` 写的是 `# enabled: true`，`config.yml` 设了 `enabled: false`，于是 `weld config check` 稳定误报。

### 最小复现

```
reference: "database:\n  # host: \"\"\n  port: 5432\n"
config:    "database:\n  host: localhost\n  port: 5432\n"
→ warn database.host     # 期望：无 finding
```

## 根因

yaml.v3 其实**保留**注释，只是挂在节点上，而比较器只看 `Content`。用 db snippet dump 出来的结构：

```
[database_test] HEAD="# PostgreSQL integration tests ..."
  [name] FOOT="# host: \"\"\n# port: 0\n# user: \"\"\n# password: \"\"\n# dsn: \"\"\n# schema_only: false"
```

也就是说，被注释的可选键**是**挂在最后一个真实节点的 `FootComment` 上，理论上可以恢复；只是 `configcheck` 从未读取注释。

## 影响

- 默认只 warn，不会硬失败；但 `--strict`（CI / `weld doctor` 门禁）会让**合法配置**失败。
- 真正的损害是**告警失焦**：用户开始忽略 `weld config check`，拼写错误这一类真正要抓的信号被淹没。
- 「未知键（疑似拼错）」和「模板没写全的可选键」无法区分，消息里那句 `or a setting missing from config.example.yml` 等于自认这一点。

## 方案对比

| 方案 | 做法 | 优点 | 代价 |
| --- | --- | --- | --- |
| **A. 读注释** | 解析 `HeadComment`/`FootComment`/`LineComment` 中形如 `key:` 的行，作为「已知可选键」，只加入 known 集合，不参与 missing 检查 | 保留模板风格；覆盖 db 场景 | 启发式：http snippet 有 `# project: the built SPA...`（`http/files/config.snippet:9`）这类散文行会被误认成键 `project`；被注释的键拿不到类型，无法做 `fail` 校验 |
| **B. 模板不再注释** | 可选键照常写出（`host: ""`），并让 missing 规则对「空值/零值参考」不告警 | schema 完整、带类型 | 要改所有 capability payload；必须同时改 missing 规则，否则满屏 missing |
| **C. 显式 schema** | 不再从 example 推断；每 capability 一份 schema，或直接解析 `internal/config` 的 `yaml:"..."` tag | 正确、可带类型、unknown/missing/类型都能查 | 改动最大；与「只读 YAML、不跑项目」的定位有张力 |
| **D. 文本兜底** | unknown 检查时，键只要在 example **原文（含注释）**出现过就放行 | 一行级改动 | 最粗糙，`# project:` 之类仍会误放行 |
| **E. 只改项目 example** | 把 `log2ban.enabled` 取消注释 | 立刻消警 | 换来所有没设它的 config 都报 `enabled is missing`；治标不治本 |

## 建议

- **小步**：方案 A 的受限版本——只认「注释块里连续、键名匹配 `^[a-z_][a-z0-9_]*$`、值看起来是 YAML 标量（引号 / 数字 / bool / 空）」的行。这样 `# host: ""` 通过，`# project: the built SPA...`（值是含空格的散文）被排除。改动集中在 `configcheck`，风险可控。
- **根治**：方案 C。让 example 回归纯文档，schema 从权威来源（Go 类型）派生，三类检查都能精确。工作量最大。
- 不建议单独做 B：它把 unknown 误报换成 missing 噪音，只是把问题挪个位置。

一句话：要在 **A（便宜但启发式）** 和 **C（正确但重）** 之间选；现状是两者都没做。

## 将来的验收标准（若动手）

1. `reference` 里注释掉、`config` 里设置的键**不再**报 `not a known setting`；
2. 真正的拼写错误（reference 任何位置都未出现）仍报 unknown；
3. `# project: ...` 这类散文注释不被当成键；
4. 现有 `configcheck_test.go` 全绿，并为上述三种情形补用例；
5. 若选 C：`weld config check` 仍不执行项目、不读取值。

## 参考

- `internal/configcheck/configcheck.go:163` `mapping()`
- `internal/configcheck/configcheck.go:183` unknown-key 告警
- `internal/configcheck/configcheck.go:190` missing-key 告警
- `internal/weldtemplate/capabilities/db/files/config.snippet:26-31` 被注释的可选键
- `internal/weldtemplate/capabilities/http/files/config.snippet:9` 会被误认的散文注释
- `internal/cli/configcheck.go` `weld config check` 命令与 `--strict`
