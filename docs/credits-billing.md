# 积分与计费

KinoTV 的商业化口径是**充值换积分、调用模型扣积分**。本文只描述这套体系的账目规则与
接口契约，界面形态见 [积分中心设计规范](wallet-design-spec.md)。

## 一、三条不可动摇的口径

1. **积分就是「分」。** 余额、流水金额、模型售价一律是 `int64`，域内不存在比例换算。
   要改「充多少钱得多少积分」，改的是充值商品的价格与到账积分，不是这个单位。
   之所以不做「1 元 = 100 积分」这类全局常数：常数一旦可调，历史余额的含义会跟着漂移，
   而用户看到的数字与当初付出的钱就对不上了。
2. **余额是账户上的一个数，流水是它的唯一解释。** 两者在同一次事务里写，
   `balance_after` 记下该次写入后的余额。对不上账时能直接定位到是哪一条造成的。
   任务行上**不存金额**：存了就有两个真相，退款也就失去了「退的就是当初扣的那笔」这个保证。
3. **幂等靠唯一约束，不靠调用方自觉。** 唯一索引
   `(user_id, kind, ref_type, ref_id)` 让重复写入必然命中已有流水，而不是重复扣分。
   任务重试与支付回调重放都会重复触发入账/出账，这是常态而不是异常。

## 二、流水种类

| Kind | 方向 | 业务引用 | 触发者 |
| --- | --- | --- | --- |
| `TASK_CHARGE` | 出账 | `TASK` + 任务 ID | 任务提交时预扣 |
| `TASK_REFUND` | 入账 | `TASK` + 任务 ID | 任务失败 / 取消时退回 |
| `TOPUP` | 入账 | `ORDER` + 订单 ID | 订单付清（本金） |
| `TOPUP_GIFT` | 入账 | `ORDER` + 订单 ID | 订单付清（平台赠送） |
| `ADMIN_ADJUST` | 双向 | `SELF` | 后台手工调整（补发、纠错、客诉补偿） |

本金与赠送刻意分成两条：合并成一条之后，「我买了多少」与「平台送我多少」无法分别统计，
退款只退本金时也就没有依据知道该退多少。

## 三、余额只减不穿

余额校验放在 `UPDATE ... WHERE balance + ? >= 0` 的条件里，而不是先读后写——读改写之间
会插进另一笔并发扣费，两个请求各自看到「够扣」，结果一起扣成负数。带条件的单条 UPDATE
由数据库保证原子，命中 0 行即余额不足，不需要额外加锁。

不足时对外是 **HTTP 402 + `reason: "insufficient_credits"`**，前端据此引导充值。
用 402 而不是 403：这是「钱不够」不是「没权限」，两件事的下一步动作完全不同。

## 四、任务计费的编排

- **预扣而不是跑完再扣。** 生成任务动辄几十秒到几十分钟，先跑后扣意味着用户可以在这段
  窗口里把余额花光，平台替他垫付。代价是失败要退。
- **先扣费再落库。** 反过来做的话，扣费失败时任务已经入队，worker 可能已经在调上游了；
  扣费在落库前失败只会让这次提交整体失败，用户重试即可，不存在半成品任务。
  扣了费却没落库（落库失败）时当场退回，这条任务不存在，没有任何后续路径会替它退款。
- **未定价直接拒绝**（409 `failed_precondition`），不放行也不免费。静默按 0 元出货，等到
  对账时才发现某批模型一直在白送，是这类系统里最难追溯的一种损失。运营想让某个模型免费，
  就把它的售价**显式配成 0**——定价域里「未定价」与「免费」本来就是两个可区分的状态。
- **退多少从流水里读**，不由调用方传。调用方手上只有「这个任务失败了」，让它自己算金额，
  上游调价之后就会退成一个不再等于当初扣款的值。
- 失败与取消路径都会被重放，因此退款允许「没有可退的东西」（任务免费，或这笔预扣早已退过）：
  端口返回 `refunded=false`，这不是错误。
- 退款失败**不上抛**：任务已经失败是既成事实，把终态一起吞掉只会更糟。失败会落一条
  `积分退回失败：…` 的任务日志，由后台按任务 ID 手工补退。

用量口径：图片按张（`count`）、视频与音频按秒（`videoSeconds`）、文本按次（提交时无法得知
token 数）。解析不出来的用量回退成 **1 个单位**，而不是 0——「按次计费」是这类模型的常态，
把未知当成 0 元会让一次真实调用白送。

计费用的平台模型标识是 **`渠道ID::模型Key`**（系统渠道）或逻辑模型 code（前台模型模式）：
不同渠道可能上架同名的上游 SKU，裸模型名会让两家的价目表串在一起。

## 五、充值到账

套餐（`billing_plans`）带两个字段：`credits`（到账积分）与 `gift_credits`（平台赠送）。
`period_days = 0` 且带积分时是**纯积分包**——只加积分、不产生订阅。

订单在下单时把这两个数**快照**到自己身上（`billing_orders.credits` / `gift_credits`）：
套餐改价、改赠送之后，已经付过款的这笔订单仍按当时承诺的量到账。

订单付清（支付回调或后台补单，两者共用 `markBillingOrderPaid`）时的顺序是
**先续期 → 再到账 → 最后落 PAID 状态**。理由一致：只要订单变成 PAID，就必然已经交付。
反过来（先落状态、后到账）会留下「已付款但没到账」且因幂等再也补不上的死局。

## 六、接口

用户端（主体恒为**当前会话账号**，查询串里的 `userId` 不是凭据）：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/finance/wallet` | `{ wallet: { userId, balance, lifetimeIn, lifetimeOut, updatedAt } }` |
| `GET` | `/api/finance/ledger` | `?page&pageSize&kind` → `{ entries, total, page, pageSize }` |
| `GET` | `/api/finance/plans` | 充值货架（在售套餐，含 `credits` / `giftCredits`） |
| `POST` | `/api/payments/orders` | `{ planCode, couponCode? }` 下单 |

后台（需管理员）：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/admin/credits/accounts` | `?keyword&page&pageSize`，按余额降序，带账号资料 |
| `GET` | `/api/admin/credits/ledger` | **必须带 `userId`**，否则 400 |
| `POST` | `/api/admin/credits/adjust` | `{ userId, amount, note }`，`note` 必填，写审计 `credit.adjust` |

用户端没有任何一个能直接改余额的接口——哪怕是他自己的。入账只来自充值到账与后台调整，
出账只来自任务提交。

## 七、开发库与生产库

积分表（`credit_accounts`、`credit_ledger_entries`）与新增的套餐/订单列，在
`EnsureCreditSchema` / `EnsureDevSchema` 里只对 **SQLite 驱动**建表：生产库的结构归
CanvasMind 的 Prisma 迁移所有，让 GORM 碰到共享库会按自己的类型推断改写列定义。

唯一索引在 `EnsureCreditSchema` 里用幂等 SQL **再声明一次**，不能只靠结构体标签：
AutoMigrate 对已存在的表只在它自己确认缺索引时才补，而扣费路径靠这个索引保证幂等——
索引一旦漏建，重复扣费会在生产上静默发生。

## 八、如何自查这套账

```bash
# 任意账号的余额应当等于其流水金额之和
sqlite3 "$AUTH_DB" "select a.user_id, a.balance, (select coalesce(sum(amount),0) from credit_ledger_entries e where e.user_id = a.user_id) as ledger_sum from credit_accounts a where a.balance <> (select coalesce(sum(amount),0) from credit_ledger_entries e where e.user_id = a.user_id);"

# 累计入账/出账是余额的推导值，同样应当对得上
sqlite3 "$AUTH_DB" "select * from credit_accounts where balance <> lifetime_in - lifetime_out;"
```

两条查询都应返回空。返回了行就说明有一次写库没有走 `AppendCreditEntries` 这条唯一入口。
