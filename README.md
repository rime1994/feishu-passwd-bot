# feishu-passwd-bot

飞书机器人，让用户通过聊天自助修改 OpenLDAP 账号密码，无需联系管理员。

---

## 功能

- 用户向机器人发送「修改密码」即可触发流程
- 自动读取用户的 LDAP 登录名（uid）并展示在卡片上
- 服务端校验密码强度（至少 8 位、字母+数字、两次一致）
- 校验通过后直接调用 LDAP `PasswordModify` 修改密码，新密码即时生效
- 所有操作结果（成功/失败/取消）都以交互卡片形式反馈

---

## 交互流程

```
用户发送「修改密码」
        │
        ▼
机器人发送表单卡片
（显示 LDAP 账号 uid）
        │
   用户填写新密码
        │
   ┌────┴────┐
取消      确认修改
  │           │
已取消卡片   ├─ 校验不通过 → 错误卡片（含重试按钮）
             └─ 校验通过  → 修改 LDAP → 成功卡片
```

---

## 前置要求

### OpenLDAP 用户映射

用户对象需要有 `employeeType` 属性，值为 `feishu_{飞书UserID}`，机器人通过此字段关联飞书账号与 LDAP 账号。

例如：
```
dn: uid=zhangsan,ou=people,dc=example,dc=com
objectClass: inetOrgPerson
uid: zhangsan
employeeType: feishu_ou_xxxxxxxx
```

### 飞书应用权限

在飞书开放平台 → 权限管理 中开通以下权限：

| 权限 | 用途 |
|------|------|
| `im:message` | 接收和发送消息 |
| `im:message.group_at_msg` | 接收群消息 |
| `contact:user.employee_id:readonly` | 通过 open_id 获取用户的飞书 user_id |

### 飞书应用配置

1. **事件订阅** → 请求地址填写：`https://<your-domain>/webhook/event`
   - 订阅事件：`接收消息 (im.message.receive_v1)`

2. **卡片回调** → 请求地址填写：`https://<your-domain>/webhook/card`

3. **机器人** → 开启机器人能力

---

## 部署

### 环境变量

复制 `.env.example` 为 `.env` 并填写：

```bash
cp .env.example .env
```

| 变量 | 必填 | 说明 |
|------|------|------|
| `FEISHU_APP_ID` | ✅ | 飞书应用 App ID |
| `FEISHU_APP_SECRET` | ✅ | 飞书应用 App Secret |
| `FEISHU_VERIFICATION_TOKEN` | ✅ | 飞书事件订阅 Verification Token |
| `FEISHU_ENCRYPT_KEY` | | 飞书消息加密 Key，不启用加密可留空 |
| `LDAP_ADDR` | ✅ | LDAP 服务地址，如 `ldap://openldap:389` |
| `LDAP_ADMIN_DN` | ✅ | LDAP 管理员 DN，如 `cn=admin,dc=example,dc=com` |
| `LDAP_ADMIN_PASS` | ✅ | LDAP 管理员密码 |
| `LDAP_BASE_DN` | ✅ | LDAP 搜索基准 DN，如 `dc=example,dc=com` |
| `PORT` | | 服务端口，默认 `8080` |

### Docker Compose（推荐）

本项目设计为与 [go-ldap-admin](https://github.com/eryajf/go-ldap-admin) 共存，接入其外部 Docker 网络，通过容器名直接访问 OpenLDAP。

```bash
docker compose up -d
```

`docker-compose.yml` 默认加入 `goldapadmin_go-ldap-admin` 外部网络。如果你的 OpenLDAP 网络名不同，修改 `docker-compose.yml` 中的 `networks.go-ldap-admin.name`。

### 手动运行

```bash
go build -o feishu-passwd-bot .
./feishu-passwd-bot
```

---

## CI / CD

推送到 `main` 分支后，GitHub Actions 自动构建并推送镜像到 GitHub Container Registry：

```
ghcr.io/<owner>/feishu-passwd-bot:latest
ghcr.io/<owner>/feishu-passwd-bot:sha-<short-sha>
```

镜像构建完成后，在服务器执行：

```bash
docker compose pull && docker compose up -d
```

---

## 本地开发 / 测试

飞书需要公网 HTTPS 地址才能推送回调，本地测试推荐使用 [ngrok](https://ngrok.com/)：

```bash
ngrok http 8080
```

将 ngrok 给出的 HTTPS 地址填入飞书应用的事件订阅和卡片回调配置中。

---

## 密码策略

服务端强制校验，不满足条件会返回错误卡片提示用户重填：

- 不能为空
- 两次输入必须一致
- 至少 8 位
- 必须同时包含字母和数字

---

## 项目结构

```
.
├── main.go              # 入口，HTTP 路由，飞书回调解析（schema 2.0 兼容）
├── handler/
│   └── handler.go       # 消息处理、卡片动作处理、LDAP 操作编排
├── card/
│   └── card.go          # 飞书交互卡片 JSON 模板（schema 2.0）
├── ldap/
│   └── ldap.go          # LDAP 连接、用户查询、密码修改
├── config/
│   └── config.go        # 环境变量配置加载
├── Dockerfile
├── docker-compose.yml
└── .env.example
```

---

## 技术说明

### 飞书卡片 Schema 2.0 兼容

飞书卡片 schema 2.0 的回调格式与 SDK 默认期望的 schema 1.0 不兼容：

- 回调数据包裹在 `event` 字段内，而非顶层平铺
- `header` 字段类型与 SDK 内部结构冲突

`main.go` 中的 `patchCardBody()` 函数在请求进入 SDK 前对 body 做规范化处理，解决此兼容问题。

### 卡片更新机制

飞书 schema 2.0 不支持通过 callback response 直接内联更新卡片，必须通过 `Im.Message.Patch` API 更新。回调处理函数立即返回空 ACK `{}`，异步通过 Patch API 更新卡片内容，避免飞书因等待响应超时而触发重试。
