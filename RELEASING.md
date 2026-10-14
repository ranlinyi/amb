# 发布到 GitHub（Releasing）

本仓库源码直接推送；**二进制不随 git 提交**（在 `.gitignore` 中排除），
而是作为 **Release 资源**单独上传。

已经构建好的二进制在：

- `dist/amb-linux-x86_64`（Linux 内嵌单文件版）
- `dist/amb-linux-x86_64-lite`（Linux 省空间部署版）
- `dist/amb-windows-x86_64.exe`（Windows 内嵌单文件版）
- `dist/SHA256SUMS`

---

## 0. 先准备一个 GitHub 令牌（PAT）

> 令牌等同于密码，只在这一步用；用完可以在同一页面 Revoke（删除）。不要提交进仓库，也不要发给不可信的人。

1. 浏览器登录 GitHub，打开 **https://github.com/settings/tokens**
2. 右上角 **Generate new token** → **Generate new token (classic)**
3. **Note** 随便填，例如 `amb-publish`；**Expiration** 选 7 days（够用，安全）
4. **Select scopes** 里把 **`repo`** 整个大项打勾（创建仓库、推送、发 Release 都靠它）
5. 拉到底点 **Generate token**，复制 `ghp_` 开头的那一长串

---

## 1. 一条命令完成「推源码 + 发 Release」（推荐）

在项目目录执行（把 `ghp_xxx` 换成你的令牌）：

```bash
cd ~/项目/普通聊天/amb-portable
GITHUB_TOKEN=ghp_xxx ./publish.sh
```

`publish.sh` 会自动：

1. 校验令牌；
2. 创建仓库 `ranlinyi/amb-portable`（已存在就复用）；
3. 把 `main` 分支推上去；
4. 创建 `v1.0.0` 的 Release（已存在就复用）；
5. 上传/更新四个文件：`amb-linux-x86_64`、`amb-linux-x86_64-lite`、
   `amb-windows-x86_64.exe`、`SHA256SUMS`。同名旧资源会先删除再上传，
   所以脚本可以重复运行。

成功后终端会打印仓库和 Release 网址。

可选自定义（一般不用）：

```bash
GITHUB_TOKEN=ghp_xxx GITHUB_OWNER=ranlinyi REPO_NAME=amb-portable TAG=v1.0.0 ./publish.sh
```

---

## 2. 手动方式（不想跑脚本时）

### 2.1 推源码

```bash
git remote add origin https://github.com/ranlinyi/amb-portable.git
git push -u origin main
```

（首次推送会提示输入用户名和密码：用户名填 `ranlinyi`，密码粘贴上面的 PAT，不要用登录密码。）

### 2.2 网页发 Release（零依赖）

1. 打开 `https://github.com/ranlinyi/amb-portable/releases/new`
2. Tag 填 `v1.0.0`，标题填 `amb 1.0.0（自包含单文件 + 省空间部署版）`
3. 把 `dist/` 里的四个文件拖进「Attach binaries」区域
4. 点 **Publish release**

### 2.3 用 `gh` CLI

```bash
gh auth login
gh release create v1.0.0 \
  dist/amb-linux-x86_64 \
  dist/amb-linux-x86_64-lite \
  dist/amb-windows-x86_64.exe \
  dist/SHA256SUMS \
  --title "amb 1.0.0（自包含单文件 + 省空间部署版）"
```

---

## 3. 只推源码、暂不发 Release

```bash
git push -u origin main
```

二进制以后随时可以补发：重新跑 `./publish.sh` 或在网页新建 Release。

---

## 4. 校验下载到的二进制

```bash
sha256sum -c dist/SHA256SUMS
```
