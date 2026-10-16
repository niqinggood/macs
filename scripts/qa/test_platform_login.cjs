'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
function fixture(fetch) {
  const context = {window: {openKittyT: (key, fallback) => fallback}, fetch, AbortController, setTimeout, clearTimeout};
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../../web/account_modal.js'), 'utf8'), context);
  let opens = 0;
  context.window.openAccountLoginModal = async () => {opens++;};
  return {window: context.window, opens: () => opens};
}
const response = (status, data) => ({ok: status >= 200 && status < 300, status, json: async () => data});
(async () => {
  const signedOut = fixture(async (url, options) => {
    assert.equal(url, '/api/account/me');
    assert.equal(options.credentials, 'same-origin');
    assert.equal(options.cache, 'no-store');
    return response(200, {authenticated: false});
  });
  assert.equal(await signedOut.window.requirePlatformAccountLogin(), false);
  assert.equal(signedOut.opens(), 1, 'An unauthenticated request opens login before submission');
  assert.equal(signedOut.window.composerLoginCheckPending, false);
  const signedIn = fixture(async () => response(200, {authenticated: true, user: {username: 'fixture'}}));
  assert.equal(await signedIn.window.requirePlatformAccountLogin(), true);
  assert.equal(signedIn.opens(), 0);
  const expired = fixture(async () => response(401, {}));
  assert.equal(await expired.window.requirePlatformAccountLogin(), false);
  assert.equal(expired.opens(), 1);
  const unavailable = fixture(async () => response(503, {}));
  await assert.rejects(unavailable.window.requirePlatformAccountLogin(), /无法确认登录状态/);
  assert.equal(unavailable.opens(), 0, 'Service failure must not be misreported as signed out');
  assert.equal(unavailable.window.composerLoginCheckPending, false);
  let release;
  const waiting = fixture(() => new Promise(resolve => {release = resolve;}));
  const first = waiting.window.requirePlatformAccountLogin();
  assert.equal(await waiting.window.requirePlatformAccountLogin(), false, 'Repeated sends cannot pass a pending sign-in check');
  release(response(200, {authenticated: false}));
  assert.equal(await first, false);
  assert.equal(waiting.opens(), 1);
  console.log('Platform sign-in regression passed: authenticated, unauthenticated, expired, unavailable and concurrent checks.');
})().catch(error => {console.error(error); process.exitCode = 1;});
