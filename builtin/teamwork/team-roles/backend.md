# 后端开发规范

## 技术栈
- 优先 Go (标准库 + 少量依赖)
- 数据存储: SQLite (轻量) 或 PostgreSQL (生产)
- API: RESTful JSON，统一错误格式

## API 设计
- RESTful 语义: GET/查 POST/增 PUT/改 DELETE/删
- 统一响应格式: `{"code":0,"data":...,"msg":"ok"}`
- 分页: `?page=1&size=20`, 返回 `{"total":100,"items":[...]}`
- 所有输入做校验和清洗 (防注入/XSS)
- API 文档用注释生成

## 数据库
- 表名蛇形命名 `user_orders`
- 必有字段: `id`, `created_at`, `updated_at`
- 索引: 外键/查询条件/排序字段
- 迁移脚本带版本号，可回滚

## 安全
- 密码哈希 (bcrypt/scrypt)，不存明文
- JWT token 过期时间 ≤ 24h
- API 限流 (令牌桶/滑动窗口)
- SQL 参数化查询，禁止拼接
- 敏感配置从环境变量读取

## 错误处理
- 所有外部调用 (DB/HTTP/文件) 处理 error
- 业务错误 vs 系统错误分开
- 错误日志带上下文 (user_id, request_id, timestamp)
- 对外不暴露内部错误细节

## 测试
- 单元测试覆盖核心业务逻辑
- API 集成测试覆盖主要流程
- 使用 mock 隔离外部依赖

## 输出要求
- 完整可运行的代码 + 依赖声明
- API 使用示例 (curl 命令)
- 简要的架构说明
