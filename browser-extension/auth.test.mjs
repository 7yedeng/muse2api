import test from "node:test";
import assert from "node:assert/strict";
import { cookieNames, handoffBody, isMuseTab, localEndpoint } from "./auth.mjs";

test("handoff cannot select a remote destination", () => {
  assert.equal(localEndpoint("18689"), "http://127.0.0.1:18689/admin/browser-session");
  for (const value of ["https://api.example.com", "18689/other", "80", "70000", "18689@evil.example"]) {
    assert.throws(() => localEndpoint(value));
  }
});
test("only the logged-in Muse site is accepted", () => {
  assert.ok(isMuseTab("https://muse.ai/chat"));
  for (const url of ["http://muse.ai/", "https://muse.ai:444/", "https://muse.ai.evil.example/", "https://user" + "@muse.ai/", "http://127.0.0.1:18689/"]) assert.equal(isMuseTab(url), false);
});
test("handoff filters to three cookie names and no browser profile", () => {
  const body = JSON.parse(handoffBody("test-pair-code-long-enough-for-validation", [
    { name: "hatch_sess", value: "fixture-session", domain: ".muse.ai" },
    { name: "unrelated", value: "fixture-unrelated", domain: ".muse.ai" }
  ]));
  assert.deepEqual(Object.keys(body).sort(), ["code", "cookies"]);
  assert.deepEqual(body.cookies, { hatch_sess: "fixture-session" });
  assert.equal(cookieNames.length, 3);
});
test("wrong domain, absent session and header injection are rejected", () => {
  const code = "test-pair-code-long-enough-for-validation";
  assert.throws(() => handoffBody(code, []));
  assert.throws(() => handoffBody(code, [{ name: "hatch_sess", value: "fixture-session", domain: "example.com" }]));
  assert.throws(() => handoffBody(code, [{ name: "hatch_sess", value: "fixture\r\ninvalid", domain: "muse.ai" }]));
});
