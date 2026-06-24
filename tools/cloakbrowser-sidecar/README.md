# CloakBrowser Sidecar

该服务为 Go 主进程提供本机 HTTP 浏览器协议。每个 `sessionId` 对应一个持久化 CloakBrowser profile，同一会话内的动作会串行执行。

## 安装与启动

需要 Node.js 20 或更高版本：

```powershell
cd tools/cloakbrowser-sidecar
npm install
npm start
```

首次启动浏览器时，CloakBrowser 会下载约 200 MB 的 Chromium。

默认仅监听 `127.0.0.1:20009`。若修改为非回环地址，必须同时设置 `BROWSER_AUTH_TOKEN`，并在 Go `config.yaml` 的 `server.browser.authToken` 中配置相同值。

常用环境变量见 `.env.example`。登录态、Cookie 和浏览历史保存在 `BROWSER_PROFILE_ROOT`。
