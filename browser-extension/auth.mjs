export const cookieNames = Object.freeze(["hatch_sess", "hatch_native_auth_device", "hatch_vml"]);

export function localEndpoint(port) {
  const text = String(port);
  if (!/^[0-9]{4,5}$/.test(text) || Number(text) < 1024 || Number(text) > 65535) {
    throw new Error("invalid_local_port");
  }
  return `http://127.0.0.1:${Number(text)}/admin/browser-session`;
}

export function isMuseTab(url) {
  try {
    const parsed = new URL(url);
    return parsed.origin === "https://muse.ai" && !parsed.username && !parsed.password;
  } catch {
    return false;
  }
}

export function handoffBody(code, records) {
  if (typeof code !== "string" || !/^[A-Za-z0-9_-]{24,128}$/.test(code)) {
    throw new Error("invalid_pair_code");
  }
  const cookies = {};
  for (const record of records) {
    if (!record || !cookieNames.includes(record.name)) continue;
    if (!["muse.ai", ".muse.ai"].includes(record.domain)) throw new Error("wrong_cookie_domain");
    if (typeof record.value !== "string" || record.value.length > 16384 || /[\r\n\0]/.test(record.value)) {
      throw new Error("invalid_cookie_value");
    }
    cookies[record.name] = record.value;
  }
  if (!cookies.hatch_sess) throw new Error("muse_session_missing");
  const body = JSON.stringify({ code, cookies });
  if (new TextEncoder().encode(body).length > 32768) throw new Error("session_too_large");
  return body;
}
