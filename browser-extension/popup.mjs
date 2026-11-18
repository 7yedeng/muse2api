import { cookieNames, handoffBody, isMuseTab, localEndpoint } from "./auth.mjs";

const button = document.getElementById("authorize");
const status = document.getElementById("status");
const codeInput = document.getElementById("code");

button.addEventListener("click", async () => {
  button.disabled = true;
  status.textContent = "正在检查当前 Muse 标签页…";
  try {
    const endpoint = localEndpoint(document.getElementById("port").value);
    const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
    if (tab?.incognito) throw new Error("private_tabs_unsupported");
    if (!isMuseTab(tab?.url)) throw new Error("open_muse_tab");
    // cookies.get selects the cookie used for the session endpoint, including
    // the most specific path. Never enumerate the browser's other cookies.
    const records = await Promise.all(cookieNames.map(name => chrome.cookies.get({ url: "https://muse.ai/api/session", name })));
    const body = handoffBody(codeInput.value.trim(), records);
    const response = await fetch(endpoint, {
      method: "POST", headers: { "Content-Type": "application/json" }, body,
      cache: "no-store", credentials: "omit", redirect: "error"
    });
    codeInput.value = "";
    if (!response.ok) {
      status.textContent = `授权未完成（HTTP ${response.status}）。请检查本机服务、重新获取授权码并确认官网账号可用。`;
      return;
    }
    status.textContent = "本机已接收会话。请回到管理页确认 ready 和加密保存状态，再测试 API。";
  } catch (error) {
    const messages = {
      open_muse_tab: "请切换到已登录的 https://muse.ai 标签页，再打开扩展。",
      private_tabs_unsupported: "当前版本请使用普通窗口中的 Muse 标签页，避免混用隐私窗口的账号。",
      invalid_local_port: "请输入 1024—65535 范围内的本机管理端口。",
      invalid_pair_code: "请从本机管理页取得本次有效的一次性授权码。",
      muse_session_missing: "没有找到当前 Muse 登录会话，请先完成正常网页登录。",
      wrong_cookie_domain: "会话来源不匹配，已停止。",
      invalid_cookie_value: "会话格式不符合要求，已停止。",
      session_too_large: "会话超过本机交接大小限制，已停止。"
    };
    status.textContent = messages[error?.message] || "无法完成本机交接。请检查服务端口与管理页状态；不要把 Cookie 或错误原文发到公开渠道。";
    codeInput.value = "";
  } finally {
    button.disabled = false;
  }
});
