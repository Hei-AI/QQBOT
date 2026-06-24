import http from "node:http";
import path from "node:path";
import { mkdir } from "node:fs/promises";
import { launchPersistentContext } from "cloakbrowser";

const host = process.env.BROWSER_HOST || "127.0.0.1";
const port = integerEnv("BROWSER_PORT", 20009);
const authToken = (process.env.BROWSER_AUTH_TOKEN || "").trim();
const profileRoot = path.resolve(process.env.BROWSER_PROFILE_ROOT || path.join(process.cwd(), "data", "browser-profiles"));
const headless = booleanEnv("BROWSER_HEADLESS", false);
const humanize = booleanEnv("BROWSER_HUMANIZE", true);
const searchBaseUrl = process.env.BROWSER_SEARCH_BASE_URL || "https://www.google.com/search?q=";
const navigationTimeoutMs = integerEnv("BROWSER_NAVIGATION_TIMEOUT_MS", 45000);
const maxBodyBytes = integerEnv("BROWSER_MAX_BODY_BYTES", 1 << 20);
const maxTextChars = integerEnv("BROWSER_MAX_TEXT_CHARS", 16000);
const sessions = new Map();

await mkdir(profileRoot, { recursive: true });

function integerEnv(name, fallback) {
  const value = Number.parseInt(process.env[name] || "", 10);
  return Number.isFinite(value) && value > 0 ? value : fallback;
}

function booleanEnv(name, fallback) {
  const value = (process.env[name] || "").trim().toLowerCase();
  if (!value) return fallback;
  return value === "1" || value === "true" || value === "yes";
}

function safeSessionId(value) {
  const normalized = String(value || "default").trim().replace(/[^a-zA-Z0-9._-]/g, "_").slice(0, 80);
  return normalized || "default";
}

function validateHttpUrl(raw) {
  let parsed;
  try {
    parsed = new URL(String(raw || "").trim());
  } catch {
    throw new Error("URL 无效");
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    throw new Error("只允许 http/https URL");
  }
  return parsed.toString();
}

async function createSession(sessionId) {
  const userDataDir = path.join(profileRoot, sessionId);
  await mkdir(userDataDir, { recursive: true });
  const context = await launchPersistentContext({
    userDataDir,
    headless,
    humanize,
    viewport: { width: 1440, height: 900 },
    locale: "zh-CN",
    timezone: "Asia/Shanghai",
    args: [`--fingerprint=${stableFingerprintSeed(sessionId)}`]
  });
  const pages = context.pages();
  const page = pages.length > 0 ? pages[pages.length - 1] : await context.newPage();
  page.setDefaultTimeout(15000);
  page.setDefaultNavigationTimeout(navigationTimeoutMs);
  return { id: sessionId, context, page, refs: new Map(), queue: Promise.resolve() };
}

function stableFingerprintSeed(sessionId) {
  let hash = 2166136261;
  for (const char of sessionId) {
    hash ^= char.charCodeAt(0);
    hash = Math.imul(hash, 16777619);
  }
  return 10000 + (Math.abs(hash) % 90000);
}

async function getSession(rawSessionId) {
  const sessionId = safeSessionId(rawSessionId);
  let session = sessions.get(sessionId);
  if (!session) {
    const creating = createSession(sessionId)
      .then((created) => {
        if (sessions.get(sessionId) === creating) sessions.set(sessionId, created);
        return created;
      })
      .catch((error) => {
        if (sessions.get(sessionId) === creating) sessions.delete(sessionId);
        throw error;
      });
    sessions.set(sessionId, creating);
    session = creating;
  }
  return await session;
}

async function withSession(rawSessionId, operation) {
  const sessionId = safeSessionId(rawSessionId);
  let session = await getSession(sessionId);
  try {
    return await queueSessionOperation(session, operation);
  } catch (error) {
    if (!isClosedBrowserError(error)) throw error;
    if (sessions.get(sessionId) === session) sessions.delete(sessionId);
    await session.context.close().catch(() => {});
    session = await getSession(sessionId);
    return await queueSessionOperation(session, operation);
  }
}

async function queueSessionOperation(session, operation) {
  const next = session.queue.then(() => operation(session));
  session.queue = next.catch(() => {});
  return await next;
}

function isClosedBrowserError(error) {
  const message = error instanceof Error ? error.message : String(error);
  return /target page, context or browser has been closed|browserContext\.newPage.*closed|page has been closed/i.test(message);
}

async function activePage(session) {
  const pages = session.context.pages().filter((page) => !page.isClosed());
  if (pages.length === 0) {
    session.page = await session.context.newPage();
  } else {
    session.page = pages[pages.length - 1];
  }
  return session.page;
}

async function pageState(session, includeText = true) {
  const page = await activePage(session);
  const state = {
    ok: true,
    sessionId: session.id,
    url: page.url(),
    title: await page.title().catch(() => "")
  };
  if (includeText) {
    state.text = await readableText(page);
    state.elements = await interactiveElements(session, page);
  }
  return state;
}

async function readableText(page) {
  const text = await page.locator("body").innerText({ timeout: 10000 }).catch(() => "");
  return text.replace(/\n{3,}/g, "\n\n").trim().slice(0, maxTextChars);
}

async function interactiveElements(session, page) {
  session.refs.clear();
  await page.locator("[data-codex-browser-ref]").evaluateAll((nodes) => {
    for (const node of nodes) node.removeAttribute("data-codex-browser-ref");
  }).catch(() => {});
  const selector = [
    "a[href]",
    "button",
    "input",
    "textarea",
    "select",
    "[role='button']",
    "[role='link']",
    "[contenteditable='true']",
    "video",
    "audio"
  ].join(",");
  const locator = page.locator(selector);
  const count = Math.min(await locator.count(), 120);
  const elements = [];
  for (let index = 0; index < count; index += 1) {
    const item = locator.nth(index);
    const visible = await item.isVisible().catch(() => false);
    if (!visible) continue;
    const ref = `e${elements.length + 1}`;
    await item.evaluate((node, value) => node.setAttribute("data-codex-browser-ref", value), ref).catch(() => {});
    const details = await item.evaluate((node) => ({
      tag: node.tagName.toLowerCase(),
      role: node.getAttribute("role") || "",
      name: (
        node.getAttribute("aria-label") ||
        node.getAttribute("title") ||
        node.getAttribute("placeholder") ||
        node.innerText ||
        node.value ||
        ""
      ).replace(/\s+/g, " ").trim().slice(0, 180),
      disabled: Boolean(node.disabled) || node.getAttribute("aria-disabled") === "true"
    })).catch(() => ({ tag: "", role: "", name: "", disabled: false }));
    session.refs.set(ref, `[data-codex-browser-ref="${ref}"]`);
    elements.push({ ref, ...details });
  }
  return elements;
}

function locatorFor(page, session, args) {
  const ref = String(args.ref || "").trim();
  if (ref) {
    const selector = session.refs.get(ref);
    if (!selector) throw new Error(`元素 ref ${ref} 已过期，请重新 browser_read`);
    return page.locator(selector);
  }
  const selector = String(args.selector || "").trim();
  if (selector) return page.locator(selector).first();
  const text = String(args.text || "").trim();
  if (text) return page.getByText(text, { exact: false }).first();
  throw new Error("需要 ref、selector 或 text");
}

async function settle(page) {
  await page.waitForLoadState("domcontentloaded", { timeout: 8000 }).catch(() => {});
  await page.waitForTimeout(500);
}

async function screenshotState(session, args = {}) {
  const page = await activePage(session);
  const bytes = await page.screenshot({
    type: "png",
    fullPage: Boolean(args.fullPage),
    animations: "disabled"
  });
  const state = await pageState(session, false);
  state.screenshotMimeType = "image/png";
  state.screenshotBase64 = bytes.toString("base64");
  return state;
}

async function executeAction(session, action, args) {
  const page = await activePage(session);
  switch (action) {
    case "navigate": {
      const url = validateHttpUrl(args.url);
      await page.goto(url, { waitUntil: "domcontentloaded" });
      await settle(page);
      return await pageState(session);
    }
    case "search": {
      const query = String(args.query || "").trim();
      if (!query) throw new Error("搜索关键词不能为空");
      await page.goto(searchBaseUrl + encodeURIComponent(query), { waitUntil: "domcontentloaded" });
      await settle(page);
      return await pageState(session);
    }
    case "read":
      return await pageState(session);
    case "click": {
      const locator = locatorFor(page, session, args);
      await locator.scrollIntoViewIfNeeded();
      await locator.click();
      await settle(page);
      return await pageState(session);
    }
    case "type": {
      const locator = locatorFor(page, session, args);
      const text = String(args.text || "");
      await locator.scrollIntoViewIfNeeded();
      if (args.clear !== false) await locator.fill("");
      await locator.type(text);
      if (args.submit) {
        await locator.press("Enter");
        await settle(page);
      }
      return await pageState(session);
    }
    case "scroll": {
      const direction = String(args.direction || "down");
      const amount = Math.min(Math.max(Number(args.amount) || 700, 100), 5000);
      if (direction === "top") await page.evaluate(() => window.scrollTo({ top: 0, behavior: "smooth" }));
      else if (direction === "bottom") await page.evaluate(() => window.scrollTo({ top: document.body.scrollHeight, behavior: "smooth" }));
      else await page.mouse.wheel(0, direction === "up" ? -amount : amount);
      await page.waitForTimeout(700);
      return await pageState(session);
    }
    case "back":
      await page.goBack({ waitUntil: "domcontentloaded" }).catch(() => null);
      await settle(page);
      return await pageState(session);
    case "next_page": {
      const candidates = [
        page.getByRole("link", { name: /下一页|下页|next|more|更多|继续/i }),
        page.getByRole("button", { name: /下一页|下页|next|more|更多|继续|加载更多/i }),
        page.locator("a[rel='next']")
      ];
      let clicked = false;
      for (const candidate of candidates) {
        const item = candidate.first();
        if (await item.isVisible().catch(() => false)) {
          await item.click();
          clicked = true;
          break;
        }
      }
      if (!clicked) throw new Error("没有找到可见的下一页或加载更多按钮");
      await settle(page);
      return await pageState(session);
    }
    case "inspect_media":
      return await inspectMedia(session, page, args);
    case "wait": {
      const milliseconds = Math.min(Math.max(Number(args.milliseconds) || 1000, 100), 30000);
      await page.waitForTimeout(milliseconds);
      return await pageState(session);
    }
    case "watch": {
      const milliseconds = Math.min(Math.max(Number(args.milliseconds) || 5000, 500), 30000);
      await page.waitForTimeout(milliseconds);
      return await screenshotState(session, args);
    }
    case "screenshot":
      return await screenshotState(session, args);
    case "close":
      await session.context.close();
      sessions.delete(session.id);
      return { ok: true, sessionId: session.id, message: "浏览器会话已关闭" };
    default:
      throw new Error(`未知浏览器动作: ${action}`);
  }
}

async function inspectMedia(session, page, args) {
  const command = String(args.command || "inspect");
  const index = Math.max(Number(args.index) || 0, 0);
  const media = page.locator("video, audio").nth(index);
  if ((await media.count()) === 0) throw new Error(`没有找到序号为 ${index} 的媒体元素`);
  if (command !== "inspect") {
    await media.evaluate(async (element, input) => {
      if (input.command === "play") await element.play();
      if (input.command === "pause") element.pause();
      if (input.command === "mute") element.muted = true;
      if (input.command === "unmute") element.muted = false;
      if (input.command === "seek" && Number.isFinite(input.seconds)) element.currentTime = input.seconds;
    }, { command, seconds: Number(args.seconds) });
  }
  const states = await page.locator("video, audio").evaluateAll((items) => items.map((element) => ({
    tag: element.tagName.toLowerCase(),
    source: element.currentSrc || element.src || "",
    currentTime: Number.isFinite(element.currentTime) ? element.currentTime : 0,
    duration: Number.isFinite(element.duration) ? element.duration : 0,
    paused: element.paused,
    muted: element.muted,
    volume: element.volume,
    readyState: element.readyState
  })));
  const state = await pageState(session, false);
  state.media = states;
  return state;
}

function authorize(request) {
  if (!authToken) return true;
  return request.headers.authorization === `Bearer ${authToken}`;
}

async function readJson(request) {
  const chunks = [];
  let size = 0;
  for await (const chunk of request) {
    size += chunk.length;
    if (size > maxBodyBytes) throw new Error("请求体过大");
    chunks.push(chunk);
  }
  return JSON.parse(Buffer.concat(chunks).toString("utf8") || "{}");
}

function sendJson(response, status, value) {
  const body = Buffer.from(JSON.stringify(value));
  response.writeHead(status, {
    "Content-Type": "application/json; charset=utf-8",
    "Content-Length": body.length,
    "Cache-Control": "no-store"
  });
  response.end(body);
}

const server = http.createServer(async (request, response) => {
  try {
    if (!authorize(request)) {
      sendJson(response, 401, { ok: false, error: "UNAUTHORIZED", message: "浏览器 sidecar 鉴权失败" });
      return;
    }
    if (request.method === "GET" && request.url === "/health") {
      sendJson(response, 200, { ok: true, sessions: sessions.size });
      return;
    }
    if (request.method !== "POST" || request.url !== "/v1/action") {
      sendJson(response, 404, { ok: false, error: "NOT_FOUND" });
      return;
    }
    const body = await readJson(request);
    const action = String(body.action || "").trim();
    const result = await withSession(body.sessionId, (session) => executeAction(session, action, body.arguments || {}));
    sendJson(response, 200, result);
  } catch (error) {
    sendJson(response, 400, {
      ok: false,
      error: "BROWSER_ACTION_FAILED",
      message: error instanceof Error ? error.message : String(error)
    });
  }
});

async function shutdown() {
  server.close();
  const openSessions = [];
  for (const value of sessions.values()) {
    openSessions.push(Promise.resolve(value).then((session) => session.context.close()).catch(() => {}));
  }
  await Promise.all(openSessions);
  process.exit(0);
}

process.on("SIGINT", shutdown);
process.on("SIGTERM", shutdown);
server.listen(port, host, () => {
  console.log(`CloakBrowser sidecar listening on http://${host}:${port}`);
});
