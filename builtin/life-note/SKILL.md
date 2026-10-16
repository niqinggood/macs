---
name: life-note
version: 1.0.0
author: OpenKitty
status: stable
description: 生活小常识笔记。当用户询问日常生活中的实用技巧、饮食健康、咖啡点单、家居妙招、省钱经验等生活类问题时触发。可通过瑞幸MCP实际下单咖啡。
---

# Life Note — 生活小常识笔记

日常实用生活知识的即查即用手册。

## IRON RULES (non-negotiable)

1. **Safety first**: For medical, first-aid, or health-related content, always include appropriate warnings and disclaimers. Never replace professional medical advice — explicitly direct to a doctor for anything beyond basic first aid.
2. **Accuracy over completeness**: If unsure about a specific detail (dosage, timing, compatibility), omit it rather than guessing. Incorrect life advice can cause real harm.
3. **Actionable only**: Every answer must contain specific, executable steps. No vague generalities, no "you could try..." without concrete instructions.
4. **No fabricated data**: Prices, deals, measurements, and factual claims must be verifiable or clearly marked as approximate. Never invent statistics or pricing.
5. **Direct and concise**: Skip pleasantries and disclaimers before the answer. Users come for quick reference, not conversation. Lead with the actionable answer.
6. **Privacy respect**: Never ask for or store personal health data, precise location data, or other sensitive personal information.

---

## 瑞幸咖啡点单（MCP 实操）

### 自动点单流程

如果已配置 `luckin` MCP server（config.yaml → mcp_servers），可直接通过 AI 完成点单全流程：

**Step 1 — 查门店**
```
mcp_luckin_queryShopList({latitude: 31.2304, longitude: 121.4737, radius: 2000})
```
获取附近门店列表，选最近的那家 `storeId`。

**Step 2 — 搜商品**
```
mcp_luckin_searchProductForMcp({query: "用户要什么", storeId: "xxx"})
```
根据用户需求模糊匹配商品。常见 query 对应：
- "热的 拿铁" → 热拿铁类
- "生椰 冰" → 冰生椰拿铁
- "推荐 热饮" → 热门热饮
- "9.9" → 9.9元专区

**Step 3 — 看详情**
```
mcp_luckin_queryProductDetailInfo({productId: "xxx", storeId: "xxx"})
```
确认规格、价格、是否有货。

**Step 4 — 预览订单**
```
mcp_luckin_previewOrder({storeId: "xxx", items: [{productId: "xxx", quantity: 1}], couponToken: ""})
```
自动匹配最优优惠券，确认最终价格。

**Step 5 — 下单**
```
mcp_luckin_createOrder({storeId: "xxx", items: [...], couponToken: "xxx"})
```
创建订单，返回取餐码。

**Step 6 — 查订单**
```
mcp_luckin_queryOrderDetailInfo({orderId: "xxx"})
```
或取消：
```
mcp_luckin_cancelOrder({orderId: "xxx"})
```

### MCP 配置（用户侧）

在 `config.yaml` 中启用并填入 Token：
```yaml
mcp_servers:
  - name: "luckin"
    url: "https://open.lkcoffee.com/mcp"
    token: "你的瑞幸Token"   # 瑞幸App → 我的 → AI开放平台 → 生成Token
    enabled: true
```

### 手动点单攻略（未配置 MCP 时的参考）

**优惠策略**：小程序新用户首杯免费；每周9.9元专区更新；周五买二送一；储值卡充100送20。

**热门推荐**：生椰拿铁(9.9-14元)、厚乳拿铁(9.9-14元)、葡萄冰萃美式(12-16元)。

**对标星巴克省钱**：生椰拿铁13元 ≈ 燕麦拿铁35元，便宜63%；美式10元 ≈ 28元，便宜64%。

## 食材保鲜

### 蔬菜

| 蔬菜 | 保鲜方法 | 期限 |
|------|---------|------|
| 绿叶菜 | 洗净沥干，厨房纸包好装保鲜袋，冷藏 | 5-7天 |
| 葱/香菜 | 根部泡水，套袋冷藏；或切碎冷冻 | 2-3周 |
| 番茄 | 常温避光存放，不要冷藏（冷藏会失去风味） | 5-7天 |
| 蘑菇 | 纸袋装好冷藏，不要用塑料袋（易出水烂） | 3-5天 |
| 土豆/洋葱 | 阴凉通风处，不要放一起（互相催熟） | 2-4周 |
| 生姜/大蒜 | 网袋挂通风处，或剥好泡油冷藏 | 1-2月 |

### 水果

- **香蕉**：用保鲜膜包住根部，悬挂保存，可延缓变黑 2-3 天
- **草莓/蓝莓**：不要提前清洗，吃之前再洗；冷藏时垫厨房纸吸潮
- **牛油果**：未熟的常温放，熟了冷藏；切开后表面涂柠檬汁防氧化变黑
- **苹果**：冷藏可存1-2月，注意不要和绿叶菜放一起（释放乙烯催熟）

### 肉类

- **分装冷冻**：买回来按顿分装，压平排气再冻，解冻更快
- **解冻**：提前一晚放冷藏解冻（最安全），或连包装泡冷水；不要常温解冻
- **识别变质**：表面发黏、有酸味、颜色暗沉 → 已变质，不可食用

## 衣物护理

### 去渍速查

| 污渍 | 方法 |
|------|------|
| 油渍 | 洗洁精直接涂抹干搓，5分钟后正常洗 |
| 血渍 | **冷水**浸泡+肥皂搓洗（千万不用热水，蛋白质遇热凝固更难洗） |
| 咖啡/茶渍 | 温盐水浸泡15分钟，再正常洗 |
| 红酒 | 撒盐吸干 → 白葡萄酒或苏打水中和 → 洗衣液洗 |
| 汗渍发黄 | 小苏打+白醋调糊敷在领口，30分钟后洗 |
| 墨水 | 酒精棉球擦拭，或用牛奶浸泡 |
| 口香糖 | 冰块敷硬后刮掉，残胶用白醋溶解 |

### 保养

- **毛衣起球**：剃毛器（别用手拔，会越拔越多）
- **羊毛衫**：手洗，平铺晾干（不要拧、不要挂晾，会变形）
- **真丝**：洗发水冷水手洗，阴干不可暴晒
- **白衣服发黄**：热水+小苏打+白醋浸泡1小时后洗

## 日常急救

- **烫伤**：流动冷水冲15分钟（不要抹牙膏、酱油），起泡不要挑破
- **小伤口**：清水冲洗 → 碘伏消毒 → 创可贴，不需要酒精（刺激太强）
- **鱼刺卡喉**：不要吞饭团，不要喝醋。张嘴看得到就自己夹出来，看不到就去医院耳鼻喉科（挂号费十几块，夹一下几秒钟）
- **流鼻血**：低头前倾，手指捏住鼻翼软骨5-10分钟（不要仰头，血会倒流）
- **眼睛里进东西**：不要揉，用清水或人工泪液冲洗；冲不掉就闭眼让泪水自然冲刷
- **蜂蜇**：用指甲或卡横着刮出毒针（不要用镊子夹，会挤更多毒液），肥皂水清洗，冰敷

## 旅行打包清单

### 随身
- 身份证/护照
- 手机+充电宝+数据线
- 少量现金（备用）
- 纸巾/湿巾
- 常用药（晕车药/止痛药/创可贴）

### 洗漱
- 分装瓶（洗衣液瓶用完洗干净装洗发水，不用买旅行装）
- 牙刷/牙膏/毛巾
- 防晒霜（即使是冬天）

### 技巧
- 鞋子用浴帽套住再放行李箱，不弄脏衣服
- 易碎品包在衣服里
- 重物放箱子底部（靠近轮子一侧）
- 腰带卷起来放衬衫领子里，领子不变形

## 居家妙招

- **微波炉清洁**：碗里放柠檬片+水，微波2分钟，蒸汽软化污垢后一擦就干净
- **冰箱除味**：放一小碟小苏打或咖啡渣
- **马桶清洁**：倒入小苏打+白醋，放置15分钟后刷洗
- **下水道疏通**：先倒半杯小苏打，再倒半杯白醋，盖住15分钟后冲热水
- **手机充电口清洁**：牙签包一点棉花轻轻掏（不要用金属针，会短路）
- **WiFi信号差**：路由器不要放角落或地上，放高处中间位置；微波炉工作时不要离路由器太近（同频干扰）

## 输出格式

根据用户具体问题，直接给出简洁实用的答案。格式要求：
- 重点突出，不啰嗦
- 给出具体可操作步骤
- 有数据时说数据
- 可用表格呈现对比信息
- 不需要"当然可以"之类的开场白，直接回答

---

## Anti-Patterns (DO NOT DO)

| # | Anti-Pattern | Why It Fails | Correct Behavior |
|---|-------------|--------------|-----------------|
| 1 | Giving medical advice beyond basic first aid | Life Note is not a doctor; wrong medical advice endangers health and creates liability | Clearly state "see a doctor if..." for anything beyond basic first aid; never prescribe |
| 2 | Rambling introductions or lengthy disclaimers before the answer | Users need quick reference in the moment; walls of text defeat the purpose | Lead with the actionable answer; place brief caveats at the end if needed |
| 3 | Recommending unverified viral "hacks" or "tricks" | Many popular life hacks are untested, ineffective, or actively harmful | Only include methods backed by reliable sources or widely accepted practice |
| 4 | Treating prices, deals, or promotions as permanent facts | Promotions change weekly; stale information misleads users into bad decisions | Always qualify with "typically," "as of [date]," or "check current pricing" |
| 5 | Overloading the user with every possible option | Decision fatigue defeats the purpose of a quick-reference skill | Give the single best recommendation first; list alternatives only if explicitly requested |

---

## Pre-Output Checklist
- [ ] Safety warnings present for any health/first-aid content
- [ ] All factual claims (prices, measurements) are verifiable or qualified
- [ ] Answer is direct and actionable — no unnecessary preamble
- [ ] No fabricated or guessed data included
- [ ] Output saved to `.openkitty/storage/`
